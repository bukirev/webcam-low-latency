package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// Session объединяет отмену и состояния одного окна/потока. active означает
// запущенный сеанс, а не подтверждение исправности драйвера или доставки пакетов.
type Session struct {
	cancel                 context.CancelFunc
	active                 atomic.Bool
	view                   *Viewer
	status, audio, network *Status
	startError             string
}

// Application владеет Walk-виджетами, sessions и черновиками только на UI thread.
// Рабочие горутины читают ConfigStore и Status, не карты виджетов/форм напрямую.
type Application struct {
	window                     *walk.MainWindow
	tabs                       *walk.TabWidget
	roleBox                    *walk.ComboBox
	host                       *walk.LineEdit
	message                    *walk.Label
	cameraStatus, screenStatus *walk.TextEdit
	selectCamera, selectScreen *walk.CheckBox
	store                      *ConfigStore
	sessions                   map[string]*Session
	getters                    map[string]map[string]func() any
	devices                    map[string][]NamedDevice
	building                   bool
	timer                      uintptr
}

var roleIDs = []string{"client", "server", "local"}

// Постоянный timer callback не захватывает конкретную панель навсегда.
// Связь HWND → Application удаляется при Dispose, включая диагностические окна.
var panelTimers sync.Map
var panelTimerCallback = syscall.NewCallback(updatePanelTimer)

func updatePanelTimer(hwnd, msg, id, stamp uintptr) uintptr {
	if panel, ok := panelTimers.Load(hwnd); ok {
		panel.(*Application).updateStatus()
	}
	return 0
}

// main закрепляет UI за OS thread до создания Walk/COM-окон.
// --autostart использует сохранённый профиль; обычный запуск не начинает захват.
func main() {

	runtime.LockOSThread()
	executableDirectory()
	stopDiagnostics := startDiagnostics()
	defer stopDiagnostics()
	if len(os.Args) > 1 && os.Args[1] == "obs" {
		if err := runOBSPanel(); err != nil {
			log.Printf("OBS PANEL ERROR: %v", err)
			walk.MsgBox(nil, "WebcamGo OBS", err.Error(), walk.MsgBoxIconError)
		}
		return
	}
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "--self-test") {
		if e := selfTest(); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--gpu-test" {
		if e := gpuSelfTest(); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return
	}
	role := "client"
	if len(os.Args) > 1 && contains(roleIDs, os.Args[1]) {
		role = os.Args[1]
	}
	app := &Application{store: newStore(loadConfig(role)), sessions: map[string]*Session{}, devices: map[string][]NamedDevice{}}
	log.Printf("MODE %s", role)
	if e := app.create(); e != nil {
		log.Printf("PANEL ERROR: %v", e)
		walk.MsgBox(nil, "Webcam Go", e.Error(), walk.MsgBoxIconError)
		return
	}
	if contains(os.Args[1:], "--autostart") {
		app.start("camera")
		app.start("screen")
	}
	app.window.Run()
}
func (a *Application) create() error {
	role := a.store.Get().Role
	index := 0
	for i, s := range roleIDs {
		if s == role {
			index = i
		}
	}
	a.building = true
	e := (MainWindow{AssignTo: &a.window, Title: "Webcam Low Latency — " + version, Font: Font{Family: "Segoe UI", PointSize: 10}, MinSize: Size{850, 600}, Size: Size{1100, 820}, Layout: VBox{}, Children: []Widget{
		Label{Text: "Камера · Экран · Звук — Go", Font: Font{Family: "Segoe UI", PointSize: 15, Bold: true}},
		Composite{Layout: HBox{}, Children: []Widget{Label{Text: "Режим"}, ComboBox{AssignTo: &a.roleBox, Model: []string{"Приёмник", "Передатчик", "Локальный просмотр"}, CurrentIndex: index, OnCurrentIndexChanged: a.changeRole}, Label{Text: "IP передатчика (пусто — поиск)"}, LineEdit{AssignTo: &a.host, Text: a.store.Get().Host}}},
		Composite{Layout: HBox{}, Children: []Widget{PushButton{Text: "▶ Запустить выбранное", OnClicked: func() {
			if a.selectCamera.Checked() {
				a.start("camera")
			}
			if a.selectScreen.Checked() {
				a.start("screen")
			}
		}}, PushButton{Text: "■ Остановить всё", OnClicked: a.stopAll}, PushButton{Text: "Применить", OnClicked: a.apply}, HSpacer{}, PushButton{Text: "Экономия CPU обоим", OnClicked: a.economy}}},
		Composite{Layout: HBox{}, Children: []Widget{
			GroupBox{Title: "Камера", Layout: VBox{}, Children: []Widget{CheckBox{AssignTo: &a.selectCamera, Text: "Запускать вместе", Checked: true}, TextEdit{AssignTo: &a.cameraStatus, Text: "Остановлено", ReadOnly: true, VScroll: true, MinSize: Size{280, 72}, MaxSize: Size{0, 72}}, Composite{Layout: HBox{}, Children: []Widget{PushButton{Text: "▶ Старт", OnClicked: func() { a.start("camera") }}, PushButton{Text: "■ Стоп", OnClicked: func() { a.stop("camera") }}, PushButton{Text: "Окно просмотра", OnClicked: func() { a.show("camera") }}, PushButton{Text: "Снимок", OnClicked: func() {
				if s := a.sessions["camera"]; s != nil && s.view != nil {
					s.view.Snapshot()
				}
			}}}}}},
			GroupBox{Title: "Экран и звук", Layout: VBox{}, Children: []Widget{CheckBox{AssignTo: &a.selectScreen, Text: "Запускать вместе", Checked: true}, TextEdit{AssignTo: &a.screenStatus, Text: "Остановлено", ReadOnly: true, VScroll: true, MinSize: Size{280, 72}, MaxSize: Size{0, 72}}, Composite{Layout: HBox{}, Children: []Widget{PushButton{Text: "▶ Старт", OnClicked: func() { a.start("screen") }}, PushButton{Text: "■ Стоп", OnClicked: func() { a.stop("screen") }}, PushButton{Text: "Окно просмотра", OnClicked: func() { a.show("screen") }}, PushButton{Text: "Снимок", OnClicked: func() {
				if s := a.sessions["screen"]; s != nil && s.view != nil {
					s.view.Snapshot()
				}
			}}}}}},
		}},
		TabWidget{AssignTo: &a.tabs}, Label{AssignTo: &a.message, Text: "Измените параметры → Применить → Запустить выбранное. Рендер: Direct3D; Python не нужен."},
	}}).Create()
	if e != nil {
		return e
	}
	a.host.SetEnabled(role == "client")
	a.building = false
	a.refreshDevices()
	if e = a.forms(a.store.Get()); e != nil {
		return e
	}
	a.window.Closing().Attach(func(cancel *bool, reason walk.CloseReason) {
		log.Printf("PANEL CLOSE reason=%d", reason)
		a.stopAll()
		_ = saveConfig(a.store.Get())
	})
	hwnd := uintptr(a.window.Handle())
	a.window.Disposing().Attach(func() {
		panelTimers.Delete(hwnd)
		if a.timer != 0 {
			invoke(syscall.NewLazyDLL("user32.dll").NewProc("KillTimer"), hwnd, a.timer)
		}
	})
	panelTimers.Store(hwnd, a)
	a.timer, _, _ = invoke(syscall.NewLazyDLL("user32.dll").NewProc("SetTimer"), hwnd, 1, 500, panelTimerCallback)
	return nil
}
func (a *Application) error(e error) {
	if e != nil {
		a.message.SetText(e.Error())
		walk.MsgBox(a.window, "Проверьте настройки", e.Error(), walk.MsgBoxIconError)
	}
}
func (a *Application) refreshDevices() {
	if ds, e := cameraDevices(); e == nil {
		a.devices["camera"] = ds
	}
	if ds, e := audioDevices(0); e == nil {
		a.devices["system_device"] = ds
		a.devices["output_device"] = ds
	}
	if ds, e := audioDevices(1); e == nil {
		a.devices["mic_device"] = ds
	}
	var ds []NamedDevice
	for i, m := range monitors() {
		ds = append(ds, NamedDevice{fmt.Sprintf("%d", i+1), m.Name})
	}
	a.devices["monitor"] = ds
	a.devices["window"] = windowDevices()
}

// forms перестраивает вкладки одной операцией с приостановленной layout.
// Иначе Walk может вычислить layout при уже удалённых pages и старом currentIndex.
// building подавляет обработчики, вызываемые программной установкой полей.
func (a *Application) forms(c Config) error {
	selectedTab := a.tabs.CurrentIndex()
	wasSuspended := a.window.Suspended()
	a.window.SetSuspended(true)
	defer a.window.SetSuspended(wasSuspended)
	a.building = true
	defer func() { a.building = false }()
	old := make([]*walk.TabPage, a.tabs.Pages().Len())
	for i := range old {
		old[i] = a.tabs.Pages().At(i)
	}
	_ = a.tabs.Pages().Clear()
	for _, p := range old {
		p.Dispose()
	}
	a.getters = map[string]map[string]func() any{"camera": {}, "screen": {}}
	for _, tab := range []struct {
		title, kind string
		fields      [][]any
	}{{"Камера", "camera", schema.Camera}, {"Экран", "screen", schema.Screen}, {"Звук", "screen", schema.Audio}} {
		values := c.Camera
		if tab.kind == "screen" {
			values = c.Screen
		}
		groups := map[string][]Widget{}
		var order []string
		for _, field := range tab.fields {
			group, key, label := field[0].(string), field[1].(string), field[2].(string)
			if c.Role == "client" && !contains(schema.Client[tab.kind], key) {
				continue
			}
			// The MF source may not expose the old DirectShow control interfaces. Do not
			// present unsupported controls as though they changed a physical camera.
			if tab.kind == "camera" && strings.Contains(group, "Аппаратные") {
				continue
			}
			if tab.kind == "camera" && key == "quality" && values.Str("codec") == "MJPG" {
				continue
			}
			if key == "audio_enabled" && c.Role == "client" {
				group = "Воспроизведение"
				label = "Принимать звук"
			}
			if key == "video_enabled" && c.Role == "client" {
				label = "Принимать экран"
			}
			if key == "block_ms" && c.Role == "client" {
				label = "Блок вывода, мс"
			}
			widget, get := a.fieldWidget(tab.kind, key, label, field[3], values)
			if _, ok := groups[group]; !ok {
				order = append(order, group)
			}
			groups[group] = append(groups[group], widget)
			a.getters[tab.kind][key] = get
		}
		var children []Widget
		if tab.kind == "screen" {
			children = append(children, PushButton{Text: "Обновить устройства", OnClicked: func() {
				draft := a.draft()
				a.refreshDevices()
				a.error(a.forms(draft))
				a.message.SetText("Список устройств обновлён. Нажмите «Применить» после выбора.")
			}})
		}
		if tab.title == "Экран" && c.Role != "client" {
			children = append(children, PushButton{Text: "Выбрать область мышью…", OnClicked: a.selectRegion}, Label{Text: "Выбор на снимке выбранного монитора или ввод координат. Окно захватывается в видимой области."})
		}
		if tab.title == "Камера" && c.Role != "client" {
			children = append(children, Label{Text: "MJPG: передача JPEG камеры без повторного сжатия. Аппаратные параметры зависят от драйвера Windows."})
		}
		for _, group := range order {
			children = append(children, GroupBox{Title: group, Layout: VBox{}, Children: groups[group]})
		}
		var page *walk.TabPage
		e := (TabPage{AssignTo: &page, Title: tab.title, Layout: VBox{MarginsZero: true}, Children: []Widget{ScrollView{Layout: VBox{}, Children: children}}}).Create(NewBuilder(a.window))
		if e != nil {
			return e
		}
		if e = a.tabs.Pages().Add(page); e != nil {
			return e
		}
	}
	if selectedTab >= 0 && selectedTab < a.tabs.Pages().Len() {
		_ = a.tabs.SetCurrentIndex(selectedTab)
	}
	return nil
}
func (a *Application) fieldWidget(kind, key, label string, options any, v Values) (Widget, func() any) {
	if _, ok := schema.Defaults[kind][key].(bool); ok {
		var box *walk.CheckBox
		return CheckBox{AssignTo: &box, Text: label, Checked: v.Bool(key)}, func() any { return box.Checked() }
	}
	var input Widget
	var getter func() any
	choices, isChoices := options.([]any)
	if ds, ok := a.devices[key]; ok && (key == "camera" || key == "monitor" || key == "window" || strings.HasSuffix(key, "_device")) {
		var box *walk.ComboBox
		value := v.Str(key)
		for _, d := range ds {
			if strings.HasSuffix(value, d.Name) {
				value = d.ID
			}
		}
		found := false
		for _, d := range ds {
			if d.ID == value {
				found = true
			}
		}
		if !found {
			label := value
			if label == "" {
				label = "Не выбрано"
			}
			ds = append([]NamedDevice{{value, label}}, ds...)
		}
		input = ComboBox{AssignTo: &box, Model: ds, BindingMember: "ID", DisplayMember: "Name", Value: value}
		getter = func() any {
			if x := box.Property("Value").Get(); x != nil {
				return fmt.Sprint(x)
			}
			return value
		}
	} else if isChoices && len(choices) > 0 {
		if _, numeric := choices[0].(float64); !numeric {
			var box *walk.ComboBox
			model := make([]string, len(choices))
			index := 0
			for i, x := range choices {
				model[i] = fmt.Sprint(x)
				if model[i] == v.Str(key) {
					index = i
				}
			}
			input = ComboBox{AssignTo: &box, Model: model, CurrentIndex: index}
			getter = func() any { return box.Text() }
		}
	}
	if input == nil {
		var edit *walk.LineEdit
		input = LineEdit{AssignTo: &edit, Text: v.Str(key)}
		getter = func() any {
			value := edit.Text()
			if _, ok := schema.Defaults[kind][key].(float64); ok {
				var number float64
				_, err := fmt.Sscan(value, &number)
				if err == nil {
					return number
				}
			}
			return value
		}
	}
	return Composite{Layout: Grid{Columns: 2}, Children: []Widget{Label{Text: label, MinSize: Size{260, 0}}, input}}, getter
}

// draft читает widgets на UI thread и копирует применённые карты.
// Просмотр draft и выбор пресета не должны менять конфигурацию работающих workers.
func (a *Application) draft() Config {
	c := a.store.Get()
	c.Camera = c.Camera.Clone()
	c.Screen = c.Screen.Clone()
	c.Host = strings.TrimSpace(a.host.Text())
	for kind, fields := range a.getters {
		target := c.Camera
		if kind == "screen" {
			target = c.Screen
		}
		for k, get := range fields {
			target[k] = get()
		}
	}
	return c
}

// apply сначала валидирует и сохраняет draft, затем публикует снимок.
// Параметры вида обновляются без рестарта, host/форматы/устройства требуют
// отмены прежнего сеанса. Это не транзакция с подтверждением от всех драйверов.
func (a *Application) apply() {
	next := a.draft()
	if e := validate(next); e != nil {
		a.error(e)
		return
	}
	old := a.store.Get()
	if e := saveConfig(next); e != nil {
		a.error(e)
		return
	}
	a.store.Set(next)
	for kind, s := range a.sessions {
		if s == nil || !s.active.Load() {
			continue
		}
		restart := old.Host != next.Host
		if kind == "camera" && next.Role != "client" {
			for _, k := range []string{"camera", "resolution", "fps", "codec"} {
				restart = restart || !reflect.DeepEqual(old.Camera[k], next.Camera[k])
			}
		}
		if kind == "screen" {
			for _, k := range []string{"video_port", "audio_port", "video_enabled", "audio_enabled", "system_enabled", "mic_enabled", "system_device", "mic_device", "output_device", "sample_rate", "channels", "block_ms", "buffer_ms"} {
				restart = restart || !reflect.DeepEqual(old.Screen[k], next.Screen[k])
			}
		}
		if restart {
			a.stop(kind)
			a.start(kind)
		} else if s.view != nil {
			s.view.ApplyWindowOptions()
		}
	}
	a.error(a.forms(next))
	a.message.SetText("Параметры применены.")
}

// economy меняет поля формы (20 FPS, decode /2), не store. Единственная точка
// вступления параметров в силу остаётся кнопка «Применить».
func (a *Application) economy() {
	c := a.draft()
	for _, v := range []Values{c.Camera, c.Screen} {
		v["receive_fps"] = "20"
		v["decode_scale"] = "2"
	}
	a.error(a.forms(c))
	a.message.SetText("Выбрана экономия CPU. Нажмите «Применить».")
}
func (a *Application) changeRole() {
	if a.building || a.roleBox == nil {
		return
	}
	i := a.roleBox.CurrentIndex()
	if i < 0 || i >= len(roleIDs) {
		return
	}
	role := roleIDs[i]
	if role == a.store.Get().Role {
		return
	}
	a.stopAll()
	_ = saveConfig(a.store.Get())
	c := loadConfig(role)
	a.store.Set(c)
	a.host.SetText(c.Host)
	a.host.SetEnabled(role == "client")
	a.error(a.forms(c))
	a.message.SetText("Режим изменён. Настройте параметры и нажмите «Применить».")
}

// start создаёт независимый context для камеры либо экрана/звука. Повторный
// старт активного сеанса только поднимает окно и не применяет незавершённый draft.
// Один процесс Go содержит несколько горутин; это не изоляция сбоев процессов.
// Reserve all ports for this window before opening its devices. If one bind
// fails, release the other so the user can retry immediately.
func reserveSenderPorts(kind string, c Config) (video, audio *net.UDPConn, err error) {
	bind := func(addr *net.UDPAddr) (*net.UDPConn, error) {
		conn, err := net.ListenUDP("udp4", addr)
		if err != nil {
			return nil, fmt.Errorf("Не удалось открыть UDP %d. Проверьте, не запущен ли другой передатчик.\n%w", addr.Port, err)
		}
		return conn, nil
	}
	if c.Role == "server" && (kind == "camera" || c.Screen.Bool("video_enabled")) {
		port := 5001
		if kind == "screen" {
			port = c.Screen.Int("video_port")
		}
		video, err = bind(&net.UDPAddr{Port: port})
		if err != nil {
			return nil, nil, err
		}
	}
	if kind == "screen" && c.Role != "client" && c.Screen.Bool("audio_enabled") {
		addr := &net.UDPAddr{Port: c.Screen.Int("audio_port")}
		if c.Role == "local" {
			addr = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
		}
		audio, err = bind(addr)
		if err != nil && video != nil {
			video.Close()
			video = nil
		}
	}
	return video, audio, err
}

func (a *Application) start(kind string) {
	log.Printf("START STREAM kind=%s role=%s", kind, a.store.Get().Role)
	if s := a.sessions[kind]; s != nil && s.active.Load() {
		if s.view != nil {
			s.view.Show()
		}
		return
	}
	c := a.store.Get()
	if e := validate(c); e != nil {
		a.error(e)
		return
	}
	videoConn, audioConn, err := reserveSenderPorts(kind, c)
	if err != nil {
		log.Printf("START FAILED kind=%s: %v", kind, err)
		a.sessions[kind] = &Session{startError: err.Error()}
		a.updateStatus()
		return
	}
	s := &Session{status: &Status{text: "Запуск…"}, audio: &Status{}, network: &Status{}}
	s.active.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = func() {
		cancel()
		if videoConn != nil {
			videoConn.Close()
		}
		if audioConn != nil {
			audioConn.Close()
		}
	}
	view, e := newViewer(kind, a.store, func() { s.active.Store(false); s.cancel() })
	if e != nil {
		s.cancel()
		a.error(e)
		return
	}
	s.view = view
	a.sessions[kind] = s
	input := newLatest[[]byte]()
	go decodeVideo(ctx, input, view, s.status)
	if kind == "camera" {
		if c.Role == "client" {
			go receiveVideo(ctx, "WC30", 5001, c.Host, input, s.status)
		} else {
			var sender *VideoSender
			if c.Role == "server" {
				sender = newVideoSender(s.network)
				go sender.runOn(ctx, "WC30", videoConn)
			}
			go retryMedia(ctx, func() {
				captureCamera(ctx, a.store, func(jpg []byte) {
					input.Put(jpg)
					if sender != nil {
						sender.input.Put(jpg)
					}
				}, s.status)
			})
		}
	} else {
		if c.Screen.Bool("video_enabled") {
			if c.Role == "client" {
				go receiveVideo(ctx, "WS40", c.Screen.Int("video_port"), c.Host, input, s.status)
			} else {
				var sender *VideoSender
				if c.Role == "server" {
					sender = newVideoSender(s.network)
					go sender.runOn(ctx, "WS40", videoConn)
				}
				go retryMedia(ctx, func() {
					captureScreen(ctx, a.store, func(jpg []byte) {
						input.Put(jpg)
						if sender != nil {
							sender.input.Put(jpg)
						}
					}, s.status)
				})
			}
		} else {
			s.status.Set("Приём / передача экрана отключены")
		}
		if c.Screen.Bool("audio_enabled") {
			if c.Role == "client" {
				go receiveAudio(ctx, a.store, s.audio)
			} else {
				go sendAudio(ctx, a.store, s.audio, audioConn)
			}
		} else {
			s.audio.Set("Звук отключён")
		}
	}
	a.message.SetText("Поток запущен. Изменения параметров передаются только кнопкой «Применить».")
}

// stop сигнализирует отмену и закрывает вид, не блокируя UI ожиданием драйвера.
// При немедленном restart старый worker может ещё освобождать устройство, поэтому
// источник запускается через retryMedia. Синхронный ReadSample может задержать выход.
func (a *Application) stop(kind string) {
	log.Printf("STOP STREAM kind=%s", kind)
	if s := a.sessions[kind]; s != nil {
		delete(a.sessions, kind)
		s.active.Store(false)
		if s.cancel != nil {
			s.cancel()
		}
		if s.view != nil {
			s.view.Close()
		}
	}
}
func (a *Application) stopAll() { a.stop("camera"); a.stop("screen") }
func (a *Application) show(kind string) {
	if s := a.sessions[kind]; s != nil && s.active.Load() {
		s.view.Show()
	} else {
		a.message.SetText("Сначала запустите нужный поток.")
	}
}
func (a *Application) updateStatus() {
	for kind, label := range map[string]*walk.TextEdit{"camera": a.cameraStatus, "screen": a.screenStatus} {
		text := "Остановлено"
		if s := a.sessions[kind]; s != nil && s.active.Load() {
			text = s.status.Get()
			if s.network.Get() != "" {
				text += " | " + s.network.Get()
			}
			if s.view != nil {
				text += " | " + s.view.status.Get()
			}
			if s.audio.Get() != "" {
				text += "\n" + s.audio.Get()
			}
		} else if s != nil && s.startError != "" {
			text = s.startError
		}
		text = strings.ReplaceAll(text, "\n", "\r\n")
		if label.Text() != text {
			label.SetText(text)
		}
	}
}
