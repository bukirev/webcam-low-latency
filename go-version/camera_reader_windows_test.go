package main

import (
	"context"
	"errors"
	"testing"
)

func TestAsyncCameraReaderHandlesFrameErrorsAndStop(t *testing.T) {
	h, callback, err := newCameraReaderCallback()
	if err != nil {
		t.Fatal(err)
	}
	defer readerRelease(h)
	defer callback.stop()

	readerReadSample(h, 0, 0, 0x100, 0, 0)
	select {
	case result := <-callback.result:
		if result.flags != 0x100 {
			t.Fatalf("unexpected stream flags: %#x", result.flags)
		}
	default:
		t.Fatal("stream tick not delivered")
	}
	readerReadSample(h, 0x800703E3, 0, 0, 0, 0)
	select {
	case result := <-callback.result:
		if hr(result.code) == nil {
			t.Fatal("camera abort was ignored")
		}
	default:
		t.Fatal("missing camera abort")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := callback.read(ctx, 0, 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("reader did not stop before requesting a frame: %v", err)
	}
	callback.stop()
	readerReadSample(h, 0, 0, 0, 0, 0)
	if len(callback.result) != 0 {
		t.Fatal("stopped reader accepted a late completion")
	}
}

func TestCameraSelectionDoesNotSwitchAfterUnplug(t *testing.T) {
	remaining := []NamedDevice{{"usb-a", "Camera A"}}
	for _, disconnected := range []string{"usb-b", "0: Camera B"} {
		if saved, index := cameraDeviceSelection(remaining, disconnected); saved != disconnected || index != -1 {
			t.Fatalf("missing selection %q resolved to %q at position %d", disconnected, saved, index)
		}
	}
	reconnected := append(remaining, NamedDevice{"usb-b", "Camera B"})
	for _, previous := range []string{"usb-b", "0: Camera B"} {
		if id, index := cameraDeviceSelection(reconnected, previous); id != "usb-b" || index != 1 {
			t.Fatalf("reconnected selection %q resolved to %q at position %d", previous, id, index)
		}
	}
}
