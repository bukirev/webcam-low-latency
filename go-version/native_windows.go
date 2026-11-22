package main

import (
	"fmt"
	"github.com/go-ole/go-ole"
	"math"
	"net"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var oleDLL = syscall.NewLazyDLL("ole32.dll")
var shlwapi = syscall.NewLazyDLL("shlwapi.dll")

func ptr[T any](p *T) unsafe.Pointer { return unsafe.Pointer(p) }
func hr(r uintptr) error {
	if int32(r) < 0 {
		return fmt.Errorf("Windows HRESULT 0x%08X", uint32(r))
	}
	return nil
}
func nativeArgs(args []any, pin *runtime.Pinner) []uintptr {
	out := make([]uintptr, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case unsafe.Pointer:
			if v != nil {
				pin.Pin(v)
			}
			out[i] = uintptr(v)
		case uintptr:
			out[i] = v
		case int:
			out[i] = uintptr(v)
		case uint:
			out[i] = uintptr(v)
		case uint32:
			out[i] = uintptr(v)
		case int32:
			out[i] = uintptr(v)
		case uint64:
			out[i] = uintptr(v)
		case int64:
			out[i] = uintptr(v)
		case nil:
			out[i] = 0
		default:
			panic(fmt.Sprintf("unsupported native argument %T", a))
		}
	}
	return out
}
func invoke(proc *syscall.LazyProc, args ...any) (uintptr, uintptr, error) {
	var pin runtime.Pinner
	defer pin.Unpin()
	words := nativeArgs(args, &pin)
	a, b, e := syscall.SyscallN(proc.Addr(), words...)
	runtime.KeepAlive(args)
	return a, b, e
}
func com(obj uintptr, method int, args ...any) (uintptr, error) {
	if obj == 0 {
		return 0, fmt.Errorf("null COM object")
	}
	vt := *(*uintptr)(unsafe.Pointer(obj))
	fn := (*[128]uintptr)(unsafe.Pointer(vt))[method]
	var pin runtime.Pinner
	defer pin.Unpin()
	words := append([]uintptr{obj}, nativeArgs(args, &pin)...)
	r, _, _ := syscall.SyscallN(fn, words...)
	runtime.KeepAlive(args)
	return r, hr(r)
}
func release(obj uintptr) {
	if obj != 0 {
		_, _ = com(obj, 2)
	}
}
func guid(s string) *ole.GUID { return ole.NewGUID(s) }
func query(obj uintptr, id *ole.GUID) (uintptr, error) {
	var p uintptr
	_, e := com(obj, 0, ptr(id), ptr(&p))
	return p, e
}
func comThread() (func(), error) {
	runtime.LockOSThread()
	r, _, _ := invoke(oleDLL.NewProc("CoInitializeEx"), 0, 0)
	if uint32(r) == 0x80010106 {
		return func() { runtime.UnlockOSThread() }, nil
	}
	if e := hr(r); e != nil {
		runtime.UnlockOSThread()
		return nil, e
	}
	return func() { invoke(oleDLL.NewProc("CoUninitialize")); runtime.UnlockOSThread() }, nil
}
func createCOM(clsid, iid *ole.GUID) (uintptr, error) {
	var p uintptr
	r, _, _ := invoke(oleDLL.NewProc("CoCreateInstance"), ptr(clsid), 0, 1, ptr(iid), ptr(&p))
	return p, hr(r)
}
func enableBroadcast(c *net.UDPConn) error {
	raw, e := c.SyscallConn()
	if e != nil {
		return e
	}
	var result error
	e = raw.Control(func(fd uintptr) {
		result = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	})
	if e != nil {
		return e
	}
	return result
}

var pixelBGRA = guid("{6fddc324-4e03-4bfe-b185-3d77768dc90f}")
var pixelBGR = guid("{6fddc324-4e03-4bfe-b185-3d77768dc90c}")
var containerJPEG = guid("{19e4a5aa-5662-4fc5-a0c0-1758028e1057}")

type PixelFrame struct {
	Width, Height int
	Pixels        []byte
	Sequence      uint64
}

var pixelPool = sync.Pool{}

func pixelBuffer(n int) []byte {
	if b, ok := pixelPool.Get().([]byte); ok && cap(b) >= n {
		return b[:n]
	}
	return make([]byte, n)
}
func freePixels(p *PixelFrame) {
	if p != nil {
		pixelPool.Put(p.Pixels[:0])
	}
}

type WIC struct{ factory uintptr }

func newWIC() (*WIC, error) {
	p, e := createCOM(guid("{cacaf262-9370-4615-a13b-9f5539da4c0a}"), guid("{ec5ec8a9-c395-4314-9c77-54d7a935ff70}"))
	return &WIC{p}, e
}
func (w *WIC) Close() { release(w.factory) }
func (w *WIC) Decode(jpg []byte, scale int) (*PixelFrame, error) {
	if len(jpg) == 0 {
		return nil, fmt.Errorf("пустой JPEG")
	}
	stream, _, _ := invoke(shlwapi.NewProc("SHCreateMemStream"), ptr(&jpg[0]), uintptr(len(jpg)))
	if stream == 0 {
		return nil, fmt.Errorf("не удалось создать поток JPEG")
	}
	defer release(stream)
	var decoder, frame uintptr
	_, e := com(w.factory, 4, stream, 0, 0, ptr(&decoder))
	if e != nil {
		return nil, e
	}
	defer release(decoder)
	_, e = com(decoder, 13, 0, ptr(&frame))
	if e != nil {
		return nil, e
	}
	defer release(frame)
	var width, height uint32
	_, e = com(frame, 3, ptr(&width), ptr(&height))
	if e != nil {
		return nil, e
	}
	if width < 1 || height < 1 || width > 16384 || height > 16384 || uint64(width)*uint64(height) > 40000000 {
		return nil, fmt.Errorf("неподдерживаемый размер JPEG")
	}
	source := frame
	var scaler uintptr
	if scale > 1 {
		width = max(1, width/uint32(scale))
		height = max(1, height/uint32(scale))
		_, e = com(w.factory, 11, ptr(&scaler))
		if e != nil {
			return nil, e
		}
		defer release(scaler)
		_, e = com(scaler, 8, source, uintptr(width), uintptr(height), 1)
		if e != nil {
			return nil, e
		}
		source = scaler
	}
	var converter uintptr
	_, e = com(w.factory, 10, ptr(&converter))
	if e != nil {
		return nil, e
	}
	defer release(converter)
	_, e = com(converter, 8, source, ptr(pixelBGRA), 0, 0, 0, 0)
	if e != nil {
		return nil, e
	}
	buffer := pixelBuffer(int(width) * int(height) * 4)
	_, e = com(converter, 7, 0, uintptr(width*4), uintptr(len(buffer)), ptr(&buffer[0]))
	runtime.KeepAlive(jpg)
	if e != nil {
		pixelPool.Put(buffer[:0])
		return nil, e
	}
	return &PixelFrame{Width: int(width), Height: int(height), Pixels: buffer}, nil
}

type propBag struct {
	Type       uint32
	Vt, ClType uint16
	Hint       uint32
	Name       *uint16
	CLSID      ole.GUID
}

func (w *WIC) EncodeBGRA(data uintptr, width, height, maxWidth, quality int) ([]byte, error) {
	var bitmap uintptr
	_, e := com(w.factory, 20, uintptr(width), uintptr(height), ptr(pixelBGRA), uintptr(width*4), uintptr(width*height*4), data, ptr(&bitmap))
	if e != nil {
		return nil, e
	}
	defer release(bitmap)
	source := bitmap
	var scaler uintptr
	if maxWidth > 0 && width > maxWidth {
		height = max(1, height*maxWidth/width)
		width = maxWidth
		_, e = com(w.factory, 11, ptr(&scaler))
		if e != nil {
			return nil, e
		}
		defer release(scaler)
		_, e = com(scaler, 8, source, uintptr(width), uintptr(height), 1)
		if e != nil {
			return nil, e
		}
		source = scaler
	}
	var converter uintptr
	_, e = com(w.factory, 10, ptr(&converter))
	if e != nil {
		return nil, e
	}
	defer release(converter)
	_, e = com(converter, 8, source, ptr(pixelBGR), 0, 0, 0, 0)
	if e != nil {
		return nil, e
	}
	var stream uintptr
	r, _, _ := invoke(oleDLL.NewProc("CreateStreamOnHGlobal"), 0, 1, ptr(&stream))
	if e = hr(r); e != nil {
		return nil, e
	}
	defer release(stream)
	var encoder uintptr
	_, e = com(w.factory, 8, ptr(containerJPEG), 0, ptr(&encoder))
	if e != nil {
		return nil, e
	}
	defer release(encoder)
	_, e = com(encoder, 3, stream, 2)
	if e != nil {
		return nil, e
	}
	var frame, bag uintptr
	_, e = com(encoder, 10, ptr(&frame), ptr(&bag))
	if e != nil {
		return nil, e
	}
	defer release(frame)
	defer release(bag)
	if bag != 0 {
		name, _ := syscall.UTF16PtrFromString("ImageQuality")
		p := propBag{Name: name}
		value := ole.VARIANT{VT: ole.VT_R4, Val: int64(math.Float32bits(float32(quality) / 100))}
		if _, e = com(bag, 4, 1, ptr(&p), ptr(&value)); e != nil {
			return nil, e
		}
	}
	if _, e = com(frame, 3, bag); e != nil {
		return nil, e
	}
	if _, e = com(frame, 4, uintptr(width), uintptr(height)); e != nil {
		return nil, e
	}
	format := *pixelBGR
	if _, e = com(frame, 6, ptr(&format)); e != nil {
		return nil, e
	}
	if _, e = com(frame, 11, converter, 0); e != nil {
		return nil, e
	}
	if _, e = com(frame, 12); e != nil {
		return nil, e
	}
	if _, e = com(encoder, 11); e != nil {
		return nil, e
	}
	var size uint64
	if _, e = com(stream, 5, 0, 2, ptr(&size)); e != nil {
		return nil, e
	}
	if size < 1 || size > 6<<20 {
		return nil, fmt.Errorf("неподдерживаемый размер JPEG")
	}
	if _, e = com(stream, 5, 0, 0, 0); e != nil {
		return nil, e
	}
	out := make([]byte, int(size))
	var got uint32
	_, e = com(stream, 3, ptr(&out[0]), uintptr(len(out)), ptr(&got))
	if e != nil {
		return nil, e
	}
	return out[:got], nil
}
