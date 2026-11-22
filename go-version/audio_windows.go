package main

import (
	"context"
	"fmt"
	"github.com/degubites/go-wca/pkg/wca"
	"github.com/go-ole/go-ole"
	"math"
	"net"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

func audioEnumerator() (*wca.IMMDeviceEnumerator, error) {
	var d *wca.IMMDeviceEnumerator
	e := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &d)
	return d, e
}
func endpointName(d *wca.IMMDevice) string {
	var ps *wca.IPropertyStore
	if d.OpenPropertyStore(wca.STGM_READ, &ps) != nil {
		return "Аудиоустройство"
	}
	defer ps.Release()
	var v wca.PROPVARIANT
	if ps.GetValue(&wca.PKEY_Device_FriendlyName, &v) != nil {
		return "Аудиоустройство"
	}
	name := v.String()
	// go-wca's PROPVARIANT.String copies and frees its LPWSTR itself.
	return name
}
func audioDevices(flow uint32) ([]NamedDevice, error) {
	done, e := comThread()
	if e != nil {
		return nil, e
	}
	defer done()
	d, e := audioEnumerator()
	if e != nil {
		return nil, e
	}
	defer d.Release()
	var collection *wca.IMMDeviceCollection
	if e = d.EnumAudioEndpoints(flow, 1, &collection); e != nil {
		return nil, e
	}
	defer collection.Release()
	var count uint32
	_ = collection.GetCount(&count)
	out := []NamedDevice{{"По умолчанию", "По умолчанию"}}
	for i := uint32(0); i < count; i++ {
		var device *wca.IMMDevice
		if collection.Item(i, &device) != nil {
			continue
		}
		var id string
		_ = device.GetId(&id)
		out = append(out, NamedDevice{id, endpointName(device)})
		device.Release()
	}
	return out, nil
}
func selectEndpoint(d *wca.IMMDeviceEnumerator, choice string, flow uint32) (*wca.IMMDevice, error) {
	var device *wca.IMMDevice
	if choice == "" || choice == "По умолчанию" {
		e := d.GetDefaultAudioEndpoint(flow, wca.EConsole, &device)
		return device, e
	}
	if e := d.GetDevice(choice, &device); e == nil {
		return device, nil
	}
	devices, e := audioDevices(flow)
	if e != nil {
		return nil, e
	}
	name := choice
	if i := strings.Index(name, ": "); i >= 0 {
		name = name[i+2:]
	}
	name = strings.TrimSuffix(name, " [Loopback]")
	for _, v := range devices {
		if v.Name == name {
			e = d.GetDevice(v.ID, &device)
			return device, e
		}
	}
	return nil, fmt.Errorf("аудиоустройство отключено: %s", name)
}

type audioEndpoint struct {
	de                   *wca.IMMDeviceEnumerator
	device               *wca.IMMDevice
	client               *wca.IAudioClient
	format               *wca.WAVEFORMATEX
	floating             bool
	channels, rate, bits int
	buffer               uint32
}

func openAudio(choice string, flow uint32, loopback bool, blockMS int) (*audioEndpoint, error) {
	a := &audioEndpoint{}
	var e error
	fail := func(e error) (*audioEndpoint, error) { a.Close(); return nil, e }
	a.de, e = audioEnumerator()
	if e != nil {
		return fail(e)
	}
	a.device, e = selectEndpoint(a.de, choice, flow)
	if e != nil {
		return fail(e)
	}
	if e = a.device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &a.client); e != nil {
		return fail(e)
	}
	if e = a.client.GetMixFormat(&a.format); e != nil {
		return fail(e)
	}
	a.rate = int(a.format.NSamplesPerSec)
	a.channels = int(a.format.NChannels)
	a.bits = int(a.format.WBitsPerSample)
	a.floating = a.format.WFormatTag == 3
	if a.format.WFormatTag == 0xfffe {
		a.floating = *(*uint32)(unsafe.Add(unsafe.Pointer(a.format), 24)) == 3
	}
	if a.bits != 8 && a.bits != 16 && a.bits != 24 && a.bits != 32 {
		return fail(fmt.Errorf("неподдерживаемый формат аудио: %d бит", a.bits))
	}
	flags := uint32(0)
	if loopback {
		flags = wca.AUDCLNT_STREAMFLAGS_LOOPBACK
	}
	duration := max(20, blockMS*2)
	if e = a.client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, wca.REFERENCE_TIME(duration*10000), 0, a.format, nil); e != nil {
		return fail(e)
	}
	if e = a.client.GetBufferSize(&a.buffer); e != nil {
		return fail(e)
	}
	return a, nil
}
func (a *audioEndpoint) Close() {
	if a.client != nil {
		_ = a.client.Stop()
		a.client.Release()
	}
	if a.format != nil {
		ole.CoTaskMemFree(uintptr(unsafe.Pointer(a.format)))
	}
	if a.device != nil {
		a.device.Release()
	}
	if a.de != nil {
		a.de.Release()
	}
}

func captureAudio(ctx context.Context, choice string, system bool, rate, channels, block int, ring *FloatRing, status *Status) {
	done, e := comThread()
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer done()
	flow := uint32(wca.ECapture)
	if system {
		flow = wca.ERender
	}
	a, e := openAudio(choice, flow, system, block)
	if e != nil {
		status.Set("Захват звука: " + e.Error())
		return
	}
	defer a.Close()
	var capture *wca.IAudioCaptureClient
	if e = a.client.GetService(wca.IID_IAudioCaptureClient, &capture); e != nil {
		status.Set(e.Error())
		return
	}
	defer capture.Release()
	if e = a.client.Start(); e != nil {
		status.Set(e.Error())
		return
	}
	convert := newResampler(a.rate, rate, channels)
	timer := time.NewTicker(time.Duration(max(2, block/2)) * time.Millisecond)
	defer timer.Stop()
	status.Set("Источник звука подключён")
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		for {
			var next uint32
			if e = capture.GetNextPacketSize(&next); e != nil {
				status.Set(e.Error())
				return
			}
			if next == 0 {
				break
			}
			var data *byte
			var frames, flags uint32
			var pos, qpc uint64
			if e = capture.GetBuffer(&data, &frames, &flags, &pos, &qpc); e != nil {
				break
			}
			var samples []float32
			if flags&2 != 0 || data == nil {
				samples = make([]float32, int(frames)*a.channels)
			} else {
				samples = decodeNative(unsafe.Slice(data, int(frames)*int(a.format.NBlockAlign)), a.bits, a.channels, a.floating)
			}
			converted := convert.Process(samples, a.channels)
			ring.Push(converted)
			_ = capture.ReleaseBuffer(frames)
		}
	}
}

func sendAudio(ctx context.Context, store *ConfigStore, status *Status) {
	c := store.Get()
	v := c.Screen
	rate, channels, block := v.Int("sample_rate"), v.Int("channels"), v.Int("block_ms")
	if rate < 1 || channels < 1 || channels > 2 {
		status.Set("Неверный формат передачи звука")
		return
	}
	listen := &net.UDPAddr{Port: v.Int("audio_port")}
	if c.Role == "local" {
		listen = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	}
	conn, e := net.ListenUDP("udp4", listen)
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	type source struct {
		kind   string
		ring   *FloatRing
		status *Status
	}
	var sources []source
	for _, kind := range []string{"system", "mic"} {
		if v.Bool(kind + "_enabled") {
			s := source{kind, newRing(rate * channels / 10), &Status{}}
			sources = append(sources, s)
			go retryMedia(ctx, func() {
				captureAudio(ctx, v.Str(kind+"_device"), kind == "system", rate, channels, block, s.ring, s.status)
			})
		}
	}
	if len(sources) == 0 {
		status.Set("Не выбран источник звука")
		return
	}
	clients := map[string]time.Time{}
	subscriptions := make(chan string, 32)
	go func() {
		buf := make([]byte, 256)
		for {
			n, from, e := conn.ReadFromUDP(buf)
			if e != nil {
				return
			}
			if c.Role != "local" && string(buf[:n]) == fmt.Sprintf("WA40 SUB %d", from.Port) {
				select {
				case subscriptions <- from.String():
				default:
				}
			}
		}
	}()
	timer := time.NewTicker(time.Duration(block) * time.Millisecond)
	defer timer.Stop()
	session := sessionID()
	var seq uint32
	remainder := 0
	stamp := time.Now()
	total := 0
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now()
	drain:
		for {
			select {
			case address := <-subscriptions:
				clients[address] = now
			default:
				break drain
			}
		}
		remainder += rate * block
		frames := remainder / 1000
		remainder %= 1000
		mixed := make([]float32, frames*channels)
		live := store.Get().Screen
		for _, s := range sources {
			part, _ := s.ring.Pop(len(mixed), 0, 0)
			gain := float32(live.Num(s.kind+"_gain") / 100)
			for i, x := range part {
				mixed[i] += x * gain
			}
		}
		if live.Bool("send_mute") {
			clear(mixed)
		}
		for k, t := range clients {
			if now.Sub(t) > 5*time.Second {
				delete(clients, k)
			}
		}
		if len(clients) > 0 {
			pcmPackets(mixed, rate, channels, session, &seq, func(p []byte) {
				for k := range clients {
					to, _ := net.ResolveUDPAddr("udp4", k)
					n, _ := conn.WriteToUDP(p, to)
					total += n
				}
			})
		}
		if now.Sub(stamp) >= time.Second {
			text := fmt.Sprintf("Звук: %d клиентов | %d Гц / %d кан. | %.2f Мбит/с", len(clients), rate, channels, float64(total)*8/now.Sub(stamp).Seconds()/1e6)
			if c.Role == "local" {
				text = "Звук: локальный захват, без воспроизведения"
			}
			for _, s := range sources {
				if strings.Contains(s.status.Get(), "0x") || strings.HasPrefix(s.status.Get(), "Захват звука:") {
					text += " | " + s.status.Get()
				}
			}
			status.Set(text)
			stamp = now
			total = 0
		}
	}
}

type audioInput struct {
	queue          *FloatRing
	rate, channels int
	packets        *Latest[AudioPacket]
}

func receiveAudio(ctx context.Context, store *ConfigStore, status *Status) {
	input := make(chan AudioPacket, 64)
	c := store.Get()
	var cancel context.CancelFunc
	var lastFormat string
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	status.Set("Звук: ожидание передатчика…")
	go retryMedia(ctx, func() { receiveAudioPackets(ctx, c.Host, c.Screen.Int("audio_port"), input, status) })
	var ring *FloatRing
	for {
		select {
		case <-ctx.Done():
			return
		case packet := <-input:
			format := fmt.Sprintf("%d/%d/%d", packet.Session, packet.Rate, packet.Channels)
			if format != lastFormat {
				if cancel != nil {
					cancel()
				}
				ring = newRing(packet.Rate * packet.Channels / 2)
				child, stop := context.WithCancel(ctx)
				cancel = stop
				go retryMedia(child, func() { playbackAudio(child, store, packet.Rate, packet.Channels, ring, status) })
				lastFormat = format
			}
			ring.Push(packet.Samples)
		}
	}
}
func receiveAudioPackets(ctx context.Context, host string, port int, out chan AudioPacket, status *Status) {
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{})
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(128 << 10)
	_ = enableBroadcast(conn)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	target := host
	if target == "" {
		target = "255.255.255.255"
	}
	addr, e := net.ResolveUDPAddr("udp4", net.JoinHostPort(target, strconv.Itoa(port)))
	if e != nil {
		status.Set(e.Error())
		return
	}
	var ping, seen time.Time
	source := ""
	var session uint64
	var last uint32
	hasLast := false
	buf := make([]byte, 1500)
	for ctx.Err() == nil {
		now := time.Now()
		if now.Sub(ping) >= time.Second {
			dest := *addr
			if source != "" {
				dest.IP = net.ParseIP(source)
			}
			_, _ = conn.WriteToUDP([]byte(fmt.Sprintf("WA40 SUB %d", conn.LocalAddr().(*net.UDPAddr).Port)), &dest)
			ping = now
		}
		if source != "" && now.Sub(seen) > 2*time.Second {
			source = ""
			hasLast = false
		}
		_ = conn.SetReadDeadline(now.Add(100 * time.Millisecond))
		n, from, e := conn.ReadFromUDP(buf)
		if e != nil {
			if ne, ok := e.(net.Error); !ok || !ne.Timeout() {
				status.Set(e.Error())
				return
			}
			continue
		}
		a, e := parseAudio(buf[:n])
		if e != nil || host != "" && !from.IP.Equal(addr.IP) {
			continue
		}
		if source != "" && (source != from.IP.String() || session != a.Session) {
			continue
		}
		if source == "" {
			source = from.IP.String()
			session = a.Session
			hasLast = false
		}
		if hasLast && !newer(a.Seq, last) {
			continue
		}
		last = a.Seq
		hasLast = true
		seen = now
		select {
		case out <- a:
		default:
			select {
			case <-out:
			default:
			}
			select {
			case out <- a:
			default:
			}
		}
	}
}
func playbackAudio(ctx context.Context, store *ConfigStore, rate, channels int, queue *FloatRing, status *Status) {
	done, e := comThread()
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer done()
	c := store.Get().Screen
	a, e := openAudio(c.Str("output_device"), wca.ERender, false, c.Int("block_ms"))
	if e != nil {
		status.Set("Воспроизведение: " + e.Error())
		return
	}
	defer a.Close()
	var render *wca.IAudioRenderClient
	if e = a.client.GetService(wca.IID_IAudioRenderClient, &render); e != nil {
		status.Set(e.Error())
		return
	}
	defer render.Release()
	convert := newResampler(rate, a.rate, a.channels)
	native := newRing(a.rate * a.channels / 2)
	target := rate * channels * c.Int("buffer_ms") / 1000
	waiting := true
	_ = a.client.Start()
	timer := time.NewTicker(time.Duration(max(2, c.Int("block_ms")/2)) * time.Millisecond)
	defer timer.Stop()
	stamp := time.Now()
	underruns := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		var padding uint32
		if e = a.client.GetCurrentPadding(&padding); e != nil {
			status.Set(e.Error())
			return
		}
		free := int(a.buffer - padding)
		if free < 1 {
			continue
		}
		missing := max(0, free-native.Len()/a.channels)
		needed := int(math.Ceil(float64(missing)*float64(rate)/float64(a.rate))) * channels
		minimum := 0
		if waiting {
			minimum = target
		}
		samples, count := queue.Pop(needed, minimum, target+rate*channels/50)
		if count > 0 {
			waiting = count < needed
			if waiting {
				underruns++
			}
		} else {
			waiting = true
		}
		native.Push(convert.Process(samples, channels))
		values, n := native.Pop(free*a.channels, 0, 0)
		live := store.Get().Screen
		gain := float32(live.Num("volume") / 100)
		if live.Bool("receive_mute") {
			gain = 0
		}
		for i := range values {
			values[i] *= gain
		}
		var data *byte
		if e = render.GetBuffer(uint32(free), &data); e != nil {
			continue
		}
		dst := unsafe.Slice(data, free*int(a.format.NBlockAlign))
		if n == 0 {
			clear(dst)
		} else {
			encodeNative(dst, values, a.bits, a.floating)
		}
		_ = render.ReleaseBuffer(uint32(free), 0)
		if time.Since(stamp) >= time.Second {
			status.Set(fmt.Sprintf("Звук: %d Гц / %d кан. | буфер %d мс | паузы %d", rate, channels, live.Int("buffer_ms"), underruns))
			stamp = time.Now()
		}
	}
}
