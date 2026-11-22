package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

const obsSettings = "settings_go_obs.json"

type obsConfig struct {
	Host                  string
	ScreenPort, AudioPort int
}

type obsFeed struct {
	name, magic             string
	inputPort, localPort    int
	network, output, format Status
}

type obsBridge struct {
	cancel    context.CancelFunc
	feeds     [3]*obsFeed
	listeners [3]net.Listener
}

func (b *obsBridge) close() {
	b.cancel()
	for _, listener := range b.listeners {
		if listener != nil {
			listener.Close()
		}
	}
}

func startOBSBridge(cfg obsConfig, cameraPort int, localPorts [3]int) (*obsBridge, error) {
	ctx, cancel := context.WithCancel(context.Background())
	b := &obsBridge{cancel: cancel, feeds: [3]*obsFeed{
		{name: "Камера", magic: "WC30", inputPort: cameraPort, localPort: localPorts[0]},
		{name: "Экран", magic: "WS40", inputPort: cfg.ScreenPort, localPort: localPorts[1]},
		{name: "Звук", magic: "WA40", inputPort: cfg.AudioPort, localPort: localPorts[2]},
	}}
	for i, f := range b.feeds {
		listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.localPort)))
		if err != nil {
			b.close()
			return nil, fmt.Errorf("%s, TCP %d: %w", f.name, f.localPort, err)
		}
		b.listeners[i] = listener
		f.localPort = listener.Addr().(*net.TCPAddr).Port
	}
	epoch := time.Now()
	for i, f := range b.feeds {
		packets := make(chan teleportPacket, 1)
		if f.magic == "WA40" {
			packets = make(chan teleportPacket, 8)
			go f.audio(ctx, cfg.Host, epoch, packets)
		} else {
			go f.video(ctx, cfg.Host, epoch, packets)
		}
		go serveTeleport(ctx, b.listeners[i], packets, &f.output)
	}
	return b, nil
}

func publishTeleport(out chan teleportPacket, p teleportPacket) {
	select {
	case out <- p:
		return
	default:
	}
	select {
	case <-out:
	default:
	}
	out <- p // only one producer owns this channel
}

func (f *obsFeed) video(ctx context.Context, host string, epoch time.Time, out chan teleportPacket) {
	input := newLatest[[]byte]()
	go receiveVideo(ctx, f.magic, f.inputPort, host, input, &f.network)
	for {
		select {
		case <-ctx.Done():
			return
		case jpg := <-input.ch:
			if len(jpg) == 0 {
				continue
			}
			now := time.Now()
			p, ok := teleportJPEG(jpg, now, now.Sub(epoch))
			if !ok {
				f.format.Set("Нужен цветной JPEG с чётной шириной и высотой")
				continue
			}
			f.format.Set("")
			publishTeleport(out, p)
		}
	}
}

func (f *obsFeed) audio(ctx context.Context, host string, epoch time.Time, out chan teleportPacket) {
	input := make(chan AudioPacket, 8)
	f.network.Set("Ожидание передатчика")
	go retryMedia(ctx, func() { receiveAudioPackets(ctx, host, f.inputPort, input, &f.network) })
	var clock teleportAudioClock
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout.C:
			f.network.Set("Ожидание передатчика")
		case p := <-input:
			now := time.Now()
			timeout.Reset(2 * time.Second)
			f.network.Set("PCM принимается")
			publishTeleport(out, teleportPCM(p, now, clock.stamp(p, now.Sub(epoch))))
		}
	}
}

var activeOBSPanel *obsPanel // UI thread only
var obsTimer = syscall.NewCallback(func(hwnd, msg, id, stamp uintptr) uintptr {
	if p := activeOBSPanel; p != nil && uintptr(p.window.Handle()) == hwnd {
		p.refresh()
	}
	return 0
})

type obsPanel struct {
	window              *walk.MainWindow
	host, screen, audio *walk.LineEdit
	status              *walk.Label
	start, stop         *walk.PushButton
	bridge              *obsBridge
}

func (p *obsPanel) refresh() {
	if p.bridge == nil {
		return
	}
	var lines []string
	for _, f := range p.bridge.feeds {
		lines = append(lines, fmt.Sprintf("%s  127.0.0.1:%d  |  %s  |  %s %s", f.name,
			f.localPort, f.output.Get(), f.network.Get(), f.format.Get()))
	}
	value := strings.Join(lines, "\r\n")
	if p.status.Text() != value {
		_ = p.status.SetText(value)
	}
}

func (p *obsPanel) stopBridge() {
	if p.bridge != nil {
		p.bridge.close()
		p.bridge = nil
	}
	p.start.SetEnabled(true)
	p.stop.SetEnabled(false)
	for _, field := range []*walk.LineEdit{p.host, p.screen, p.audio} {
		field.SetEnabled(true)
	}
	_ = p.status.SetText("Приём остановлен")
}

func (p *obsPanel) startBridge() {
	host := strings.TrimSpace(p.host.Text())
	screen, err1 := strconv.Atoi(strings.TrimSpace(p.screen.Text()))
	audio, err2 := strconv.Atoi(strings.TrimSpace(p.audio.Text()))
	if err1 != nil || err2 != nil || screen < 1024 || screen > 65535 || audio < 1024 || audio > 65535 ||
		screen == audio || screen == 5001 || audio == 5001 || len(host) > 253 || strings.ContainsAny(host, " \r\n\t") {
		_ = p.status.SetText("Проверьте адрес и порты. Порты должны быть разными и отличаться от 5001.")
		return
	}
	cfg := obsConfig{host, screen, audio}
	bridge, err := startOBSBridge(cfg, 5001, [3]int{16601, 16602, 16603})
	if err != nil {
		_ = p.status.SetText(err.Error())
		return
	}
	p.bridge = bridge
	p.start.SetEnabled(false)
	p.stop.SetEnabled(true)
	for _, field := range []*walk.LineEdit{p.host, p.screen, p.audio} {
		field.SetEnabled(false)
	}
	p.refresh()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(obsSettings, data, 0644); err != nil {
		walk.MsgBox(p.window, "Профиль OBS", err.Error(), walk.MsgBoxIconWarning)
	}
}

func runOBSPanel() error {
	c := loadConfig("client")
	cfg := obsConfig{c.Host, c.Screen.Int("video_port"), c.Screen.Int("audio_port")}
	if data, err := os.ReadFile(obsSettings); err == nil {
		var saved obsConfig
		if json.Unmarshal(data, &saved) == nil {
			cfg = saved
		}
	}
	p := &obsPanel{}
	err := (MainWindow{
		AssignTo: &p.window, Title: "WebcamGo OBS — приёмник", Size: Size{760, 300},
		MinSize: Size{720, 280}, Font: Font{Family: "Segoe UI", PointSize: 10}, Layout: VBox{},
		Children: []Widget{
			Composite{Layout: Grid{Columns: 2}, Children: []Widget{
				Label{Text: "IP передатчика (пусто — поиск)"}, LineEdit{AssignTo: &p.host, Text: cfg.Host},
				Label{Text: "UDP-порт экрана"}, LineEdit{AssignTo: &p.screen, Text: strconv.Itoa(cfg.ScreenPort)},
				Label{Text: "UDP-порт звука"}, LineEdit{AssignTo: &p.audio, Text: strconv.Itoa(cfg.AudioPort)},
			}},
			Composite{Layout: HBox{}, Children: []Widget{
				PushButton{AssignTo: &p.start, Text: "Запустить приём", OnClicked: p.startBridge},
				PushButton{AssignTo: &p.stop, Text: "Остановить", Enabled: false, OnClicked: p.stopBridge}, HSpacer{},
			}},
			Label{Text: "Teleport · Manual IP: камера 127.0.0.1:16601, экран :16602, звук :16603"},
			Label{AssignTo: &p.status, Text: "Приём остановлен", MinSize: Size{640, 72}},
		},
	}).Create()
	if err != nil {
		return err
	}
	activeOBSPanel = p
	proc := syscall.NewLazyDLL("user32.dll")
	hwnd := uintptr(p.window.Handle())
	timer, _, err := invoke(proc.NewProc("SetTimer"), hwnd, 1, 500, obsTimer)
	if timer == 0 {
		activeOBSPanel = nil
		p.window.Dispose()
		return fmt.Errorf("SetTimer: %w", err)
	}
	p.window.Disposing().Attach(func() {
		invoke(proc.NewProc("KillTimer"), hwnd, timer)
		activeOBSPanel = nil
		if p.bridge != nil {
			p.bridge.close()
		}
	})
	p.window.Run()
	return nil
}
