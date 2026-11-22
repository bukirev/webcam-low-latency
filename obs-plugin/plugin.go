package main

// #include "bridge.h"
import "C"

import (
	"bytes"
	"context"
	"runtime"
	"runtime/cgo"
	"strings"
	"sync"
	"time"
	"unsafe"
)

type obsWorker struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// OBS looks up these symbols by name. Go's Windows DLL linker exports the
// //export functions, so module entry points are forwarded to C from here.
//
//export obs_module_set_pointer
func obs_module_set_pointer(_ *C.obs_module_t) {}

//export obs_module_ver
func obs_module_ver() C.uint32_t { return C.webcam_module_version() }

//export obs_module_load
func obs_module_load() C.bool { return C.webcam_module_load() }

//export webcam_start
func webcam_start(source *C.obs_source_t, kind C.int, host *C.char, port, fps, scale C.int) C.uintptr_t {
	ctx, cancel := context.WithCancel(context.Background())
	w := &obsWorker{cancel: cancel}
	address := strings.TrimSpace(C.GoString(host))
	if port <= 0 || port > 65535 {
		cancel()
		return 0
	}
	w.wg.Add(2)
	if kind == 2 {
		input := make(chan AudioPacket, 8)
		go func() {
			defer w.wg.Done()
			retryMedia(ctx, func() { receiveAudioPackets(ctx, address, int(port), input, &Status{}) })
		}()
		go func() { defer w.wg.Done(); outputOBSAudio(ctx, source, input) }()
	} else {
		input := newLatest[[]byte]()
		magic := "WC30"
		if kind == 1 {
			magic = "WS40"
		}
		go func() { defer w.wg.Done(); receiveVideo(ctx, magic, int(port), address, input, &Status{}) }()
		go func() { defer w.wg.Done(); outputOBSVideo(ctx, source, input, int(fps), int(scale)) }()
	}
	return C.uintptr_t(cgo.NewHandle(w))
}

//export webcam_stop
func webcam_stop(value C.uintptr_t) {
	if value == 0 {
		return
	}
	handle := cgo.Handle(value)
	worker := handle.Value().(*obsWorker)
	worker.cancel()
	worker.wg.Wait()
	handle.Delete()
}

func outputOBSVideo(ctx context.Context, source *C.obs_source_t, input *Latest[[]byte], fps, scale int) {
	done, err := comThread()
	if err != nil {
		return
	}
	defer done()
	codec, err := newWIC()
	if err != nil {
		return
	}
	defer codec.Close()
	if scale != 2 && scale != 4 {
		scale = 1
	}
	var next time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case jpg := <-input.ch:
			if ctx.Err() != nil {
				return
			}
			if len(jpg) == 0 {
				C.webcam_video(source, nil, 0, 0)
				continue
			}
			if !bool(C.obs_source_showing(source)) {
				continue
			}
			now := time.Now()
			if fps > 0 && now.Before(next) {
				continue
			}
			// WIC can sometimes decode camera MJPG without EOI. Append the marker
			// for those samples and let the decoder reject corrupt scan data.
			if !bytes.Contains(jpg, []byte{0xff, 0xd9}) {
				jpg = append(bytes.Clone(jpg), 0xff, 0xd9)
			}
			frame, err := codec.Decode(jpg, scale)
			if err != nil {
				continue
			}
			if fps > 0 {
				next = now.Add(time.Second / time.Duration(fps))
			}
			// libobs copies the frame during this call. No Go pointer is retained.
			C.webcam_video(source, (*C.uint8_t)(unsafe.Pointer(&frame.Pixels[0])), C.uint32_t(frame.Width), C.uint32_t(frame.Height))
			runtime.KeepAlive(frame)
			freePixels(frame)
		}
	}
}

func outputOBSAudio(ctx context.Context, source *C.obs_source_t, input <-chan AudioPacket) {
	var clock audioSampleClock
	for {
		select {
		case <-ctx.Done():
			return
		case packet := <-input:
			if ctx.Err() != nil {
				return
			}
			stamp := clock.stamp(packet, time.Duration(C.webcam_now()))
			C.webcam_audio(source, (*C.float)(unsafe.Pointer(&packet.Samples[0])),
				C.uint32_t(packet.Frames), C.uint32_t(packet.Rate), C.uint32_t(packet.Channels), C.uint64_t(stamp))
			runtime.KeepAlive(packet.Samples)
		}
	}
}

func main() {}
