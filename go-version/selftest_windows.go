package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"time"
)

// Runs diagnostic work only when explicitly requested with --self-test. No
// microphone or camera contents are written to disk or uploaded.
// selfTest — явная аппаратная диагностика с кратковременным доступом к устройствам.
// Некоторые недоступные устройства записываются как *_error без ненулевого exit:
// потребитель отчёта обязан проверять JSON, а не только код завершения.
func selfTest() error {

	report := map[string]any{"version": version, "checks": map[string]any{}}
	checks := report["checks"].(map[string]any)
	defer func() {
		b, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile("go-self-test.json", b, 0644)
		fmt.Println(string(b))
	}()
	done, e := comThread()
	if e != nil {
		return e
	}
	defer done()
	panel := &Application{store: newStore(defaultConfig("client")), sessions: map[string]*Session{}, devices: map[string][]NamedDevice{}}
	if e := panel.create(); e != nil {
		checks["panel_error"] = e.Error()
		return e
	}
	defer panel.window.Dispose()
	for _, role := range roleIDs {
		cfg := defaultConfig(role)
		panel.store.Set(cfg)
		if e := panel.forms(cfg); e != nil {
			return fmt.Errorf("панель %s: %w", role, e)
		}
		if e := validate(panel.draft()); e != nil {
			return fmt.Errorf("настройки %s: %w", role, e)
		}
		if role == "client" {
			for kind, fields := range panel.getters {
				for key := range fields {
					if !contains(schema.Client[kind], key) {
						return fmt.Errorf("лишнее поле приёмника: %s", key)
					}
				}
			}
		}
	}
	checks["panel_roles"] = roleIDs

	w, e := newWIC()
	if e != nil {
		return e
	}
	defer w.Close()
	img := image.NewRGBA(image.Rect(0, 0, 128, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 128; x++ {
			img.SetRGBA(x, y, color.RGBA{220, 90, 30, 255})
		}
	}
	var data bytes.Buffer
	_ = jpeg.Encode(&data, img, &jpeg.Options{Quality: 90})

	frame, e := w.Decode(data.Bytes(), 2)
	if e != nil {
		checks["wic_decode_error"] = e.Error()
		return e
	}
	defer freePixels(frame)
	if frame.Width != 64 || frame.Height != 48 {
		return fmt.Errorf("неверный размер WIC")
	}
	checks["wic_decode"] = fmt.Sprintf("%dx%d", frame.Width, frame.Height)

	encoded := data.Bytes()
	encoded, e = w.EncodeBGRA(uintptr(ptr(&frame.Pixels[0])), frame.Width, frame.Height, 0, 80)
	if e != nil {
		checks["wic_encode_error"] = e.Error()
		return e
	}
	decoded, e := w.Decode(encoded, 1)
	if e != nil {
		return e
	}
	freePixels(decoded)
	checks["wic_encode_bytes"] = len(encoded)

	view, e := newViewer("camera", newStore(defaultConfig("client")), nil)
	if e != nil {
		checks["gpu_error"] = e.Error()
		return e
	}
	view.mu.Lock()
	view.sequence = 1
	testFrame := *frame
	testFrame.Sequence = 1
	view.frame = &testFrame
	bounds := view.canvas.ClientBoundsPixels()
	view.gpu.reading = true
	e = view.gpu.Paint(view.canvas.Handle(), max(320, bounds.Width), max(240, bounds.Height), &testFrame, defaultConfig("client").Camera)
	view.gpu.reading = false
	if e == nil {
		var rendered *PixelFrame
		rendered, e = view.gpu.readback()
		if e == nil {
			i := (rendered.Height/2*rendered.Width + rendered.Width/2) * 4
			if rendered.Pixels[i+2] < 180 || rendered.Pixels[i+1] < 60 {
				e = fmt.Errorf("неверный цвет GPU")
			}
			checks["gpu_snapshot"] = fmt.Sprintf("%dx%d", rendered.Width, rendered.Height)
		}
	}

	view.frame = nil
	view.mu.Unlock()
	view.Close()

	if e != nil {
		checks["gpu_error"] = e.Error()
		return e
	}
	checks["direct3d"] = true

	if ds, e := cameraDevices(); e == nil {
		checks["cameras"] = len(ds)
		if len(ds) > 0 {
			cfg := defaultConfig("local")
			cfg.Camera["camera"] = "0"
			cfg.Camera["resolution"] = "640x480"
			ctx, stop := context.WithTimeout(context.Background(), 8*time.Second)
			got := make(chan int, 1)
			ended := make(chan struct{})
			status := &Status{}
			go func() {
				defer close(ended)
				captureCamera(ctx, newStore(cfg), func(jpg []byte) {
					select {
					case got <- len(jpg):
					default:
					}
				}, status, nil)
			}()
			select {
			case n := <-got:
				checks["camera_jpeg_bytes"] = n
			case <-ended:
				checks["camera_capture_error"] = status.Get()
			case <-ctx.Done():
				checks["camera_capture_error"] = "таймаут: " + status.Get()
			}
			stop()
			select {
			case <-ended:
			case <-time.After(time.Second):
			}
		}
	} else {
		checks["camera_enumeration_error"] = e.Error()
	}

	for _, item := range []struct {
		name string
		flow uint32
	}{{"audio_outputs", 0}, {"audio_inputs", 1}} {
		ds, e := audioDevices(item.flow)
		if e != nil {
			checks[item.name+"_error"] = e.Error()
		} else {
			checks[item.name] = len(ds) - 1
		}
	}
	// Validate native screen capture/encoding briefly without retaining a screen image.

	c := defaultConfig("local")
	c.Screen["max_width"] = "320"
	c.Screen["fps"] = "10"
	store := newStore(c)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	status := &Status{}
	got := make(chan int, 1)
	go captureScreen(ctx, store, func(jpg []byte) {
		select {
		case got <- len(jpg):
		default:
		}
	}, status, nil)
	select {
	case n := <-got:
		checks["screen_jpeg_bytes"] = n
	case <-ctx.Done():
		checks["screen_error"] = status.Get()
		cancel()
		return fmt.Errorf("захват экрана: %s", status.Get())
	}
	cancel()
	// Validate output initialization in shared mode without emitting a tone.

	if output, e := openAudio("По умолчанию", 0, false, 10); e == nil {
		checks["wasapi_output"] = fmt.Sprintf("%dHz/%dch/%dbit", output.rate, output.channels, output.bits)
		output.Close()
	} else {
		checks["wasapi_output_error"] = e.Error()
	}
	for _, system := range []bool{false, true} {
		ctx, stop := context.WithTimeout(context.Background(), 1100*time.Millisecond)
		ring := newRing(48000 * 2)
		status := &Status{}
		ended := make(chan struct{})
		go func() {
			defer close(ended)
			captureAudio(ctx, "По умолчанию", system, 48000, 2, 10, ring, status)
		}()
		<-ctx.Done()
		stop()
		<-ended
		name := "microphone"
		if system {
			name = "system_loopback"
		}
		checks[name] = map[string]any{"status": status.Get(), "samples": ring.Len()}
	}
	ctx, stop := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	mute := defaultConfig("client")
	mute.Screen["receive_mute"] = true
	ring := newRing(48000 * 2)
	ring.Push(make([]float32, 48000*2))
	status = &Status{}
	playbackAudio(ctx, newStore(mute), 48000, 2, ring, status)
	stop()
	checks["muted_playback"] = status.Get()
	return nil
}

// gpuSelfTest проверяет минимальный путь D3D без запуска захвата.
// Он не заменяет проверку decoder, потери device и длительную передачу.
func gpuSelfTest() error {
	view, e := newViewer("camera", newStore(defaultConfig("client")), nil)
	if e != nil {
		return e
	}
	defer view.Close()
	pixels := make([]byte, 64*48*4)
	for i := 0; i < len(pixels); i += 4 {
		pixels[i] = 30
		pixels[i+1] = 90
		pixels[i+2] = 220
		pixels[i+3] = 255
	}
	f := &PixelFrame{Width: 64, Height: 48, Pixels: pixels, Sequence: 1}
	return view.gpu.Paint(view.canvas.Handle(), 640, 480, f, defaultConfig("client").Camera)
}
