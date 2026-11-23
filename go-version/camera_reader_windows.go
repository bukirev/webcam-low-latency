package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/lxn/win"
)

var (
	readerAsyncCallback    = guid("{1e3dbeac-bb43-4c35-b507-cd644464c965}")
	readerCallbackIID      = guid("{deec8d99-fa1d-4d82-84c2-2c8969944867}")
	comUnknownIID          = guid("{00000000-0000-0000-c000-000000000046}")
	readerLeavesSourceOpen = guid("{56b67165-219e-456d-a22e-2d3004c7fe56}")
	readerCallbacks        sync.Map
	readerCallbackTable    [6]uintptr
)

func init() {
	// These entry points are shared by all readers and reconnect attempts.
	readerCallbackTable = [6]uintptr{
		syscall.NewCallback(readerQueryInterface), syscall.NewCallback(readerAddRef),
		syscall.NewCallback(readerRelease), syscall.NewCallback(readerReadSample),
		syscall.NewCallback(func(_, _ uintptr) uintptr { return 0 }),
		syscall.NewCallback(func(_, _, _ uintptr) uintptr { return 0 }),
	}
}

type cameraReadResult struct {
	sample uintptr
	flags  uint32
	code   uintptr
}

type cameraReaderCallback struct {
	refs   atomic.Int32
	mu     sync.Mutex
	closed bool
	result chan cameraReadResult
}

func newCameraReaderCallback() (uintptr, *cameraReaderCallback, error) {
	h := uintptr(win.GlobalAlloc(0, unsafe.Sizeof(uintptr(0))))
	if h == 0 {
		return 0, nil, errors.New("не удалось создать callback камеры")
	}
	*(*uintptr)(unsafe.Pointer(h)) = uintptr(unsafe.Pointer(&readerCallbackTable[0]))
	c := &cameraReaderCallback{result: make(chan cameraReadResult, 1)}
	c.refs.Store(1)
	readerCallbacks.Store(h, c)
	return h, c, nil
}

func readerQueryInterface(h, iid, out uintptr) uintptr {
	if out == 0 {
		return 0x80004003 // E_POINTER
	}
	p := (*uintptr)(unsafe.Pointer(out))
	*p = 0
	if iid == 0 {
		return 0x80004003
	}
	id := *(*ole.GUID)(unsafe.Pointer(iid))
	if id != *readerCallbackIID && id != *comUnknownIID {
		return 0x80004002 // E_NOINTERFACE
	}
	readerAddRef(h)
	*p = h
	return 0
}

func readerAddRef(h uintptr) uintptr {
	value, _ := readerCallbacks.Load(h)
	return uintptr(value.(*cameraReaderCallback).refs.Add(1))
}

func readerRelease(h uintptr) uintptr {
	value, _ := readerCallbacks.Load(h)
	c := value.(*cameraReaderCallback)
	count := c.refs.Add(-1)
	if count == 0 {
		readerCallbacks.Delete(h)
		win.GlobalFree(win.HGLOBAL(h))
	}
	return uintptr(count)
}

func readerReadSample(h, code, _, flags, _, sample uintptr) uintptr {
	value, _ := readerCallbacks.Load(h)
	c := value.(*cameraReaderCallback)
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed && len(c.result) == 0 {
		if sample != 0 {
			// The native reader releases its own reference when OnReadSample returns.
			_, _ = com(sample, 1)
		}
		c.result <- cameraReadResult{sample, uint32(flags), code}
	}
	return 0
}

func (c *cameraReaderCallback) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	select {
	case result := <-c.result:
		release(result.sample)
	default:
	}
}

func (c *cameraReaderCallback) read(ctx context.Context, reader, stream uintptr, deadline *time.Timer) (cameraReadResult, error) {
	if err := ctx.Err(); err != nil {
		return cameraReadResult{}, err
	}
	// Async ReadSample takes NULL output arguments. Only one request is outstanding.
	if _, err := com(reader, 9, stream, 0, 0, 0, 0, 0); err != nil {
		return cameraReadResult{}, err
	}
	deadline.Reset(5 * time.Second)
	defer deadline.Stop()
	select {
	case <-ctx.Done():
		return cameraReadResult{}, ctx.Err()
	case <-deadline.C:
		return cameraReadResult{}, errors.New("устройство не вернуло кадр за 5 с")
	case result := <-c.result:
		return result, hr(result.code)
	}
}
