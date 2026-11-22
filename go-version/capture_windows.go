package main

import (
	"context"
	"fmt"
	"github.com/lxn/win"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var mf = syscall.NewLazyDLL("mfplat.dll")
var mfDevice = syscall.NewLazyDLL("mf.dll")
var mfRead = syscall.NewLazyDLL("mfreadwrite.dll")
var devType = guid("{c60ac5fe-252a-478f-a0ef-bc8fa5f7cad3}")
var videoType = guid("{8ac3587a-4ae7-42d8-99e0-0a6013eef90f}")
var friendly = guid("{60d0e559-52f8-4fa2-bbce-acdb34a8ec01}")
var subType = guid("{f7e34c9a-42e8-4714-b74b-cb29d72c35e5}")
var frameSize = guid("{1652c33d-d6b2-4012-b834-72030849a37d}")
var frameRate = guid("{c459a2e8-3d2c-4e44-b132-fee5156c7bb0}")
var iidMediaSource = guid("{279a808d-aec7-40c8-9c6b-a6b492c78a66}")
var mjpgGUID = guid("{47504a4d-0000-0010-8000-00aa00389b71}")

type NamedDevice struct{ ID, Name string }

func mfStart() (func(), error) {
	done, e := comThread()
	if e != nil {
		return nil, e
	}
	r, _, _ := invoke(mf.NewProc("MFStartup"), 0x20070, 0)
	if e = hr(r); e != nil {
		done()
		return nil, e
	}
	return func() { invoke(mf.NewProc("MFShutdown")); done() }, nil
}
func mfDevices() ([]uintptr, error) {
	var attrs uintptr
	r, _, _ := invoke(mf.NewProc("MFCreateAttributes"), ptr(&attrs), 1)
	if e := hr(r); e != nil {
		return nil, e
	}
	defer release(attrs)
	if _, e := com(attrs, 24, ptr(devType), ptr(videoType)); e != nil {
		return nil, e
	}
	var array uintptr
	var count uint32
	r, _, _ = invoke(mfDevice.NewProc("MFEnumDeviceSources"), attrs, ptr(&array), ptr(&count))
	if e := hr(r); e != nil {
		return nil, e
	}
	defer invoke(oleDLL.NewProc("CoTaskMemFree"), array)
	if count > 100 {
		return nil, fmt.Errorf("слишком много камер")
	}
	result := append([]uintptr(nil), unsafe.Slice((*uintptr)(unsafe.Pointer(array)), int(count))...)
	return result, nil
}
func cameraDevices() ([]NamedDevice, error) {
	done, e := mfStart()
	if e != nil {
		return nil, e
	}
	defer done()
	devices, e := mfDevices()
	if e != nil {
		return nil, e
	}
	var result []NamedDevice
	for i, d := range devices {
		var name *uint16
		var n uint32
		_, err := com(d, 13, ptr(friendly), ptr(&name), ptr(&n))
		title := "Камера " + strconv.Itoa(i)
		if err == nil && name != nil {
			title = syscall.UTF16ToString(unsafe.Slice(name, int(n)+1))
			invoke(oleDLL.NewProc("CoTaskMemFree"), uintptr(unsafe.Pointer(name)))
		}
		result = append(result, NamedDevice{strconv.Itoa(i), title})
		release(d)
	}
	return result, nil
}

func captureCamera(ctx context.Context, store *ConfigStore, publish func([]byte), status *Status) {
	done, e := mfStart()
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer done()
	devices, e := mfDevices()
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer func() {
		for _, d := range devices {
			release(d)
		}
	}()
	c := store.Get().Camera
	index, _ := strconv.Atoi(strings.Split(c.Str("camera"), ":")[0])
	if index < 0 || index >= len(devices) {
		status.Set("Выбранная камера недоступна")
		return
	}
	var source uintptr
	if _, e = com(devices[index], 33, ptr(iidMediaSource), ptr(&source)); e != nil {
		status.Set(e.Error())
		return
	}
	defer release(source)
	defer func() { _, _ = com(source, 12) }() // IMFMediaSource.Shutdown
	var reader, attrs uintptr
	r, _, _ := invoke(mf.NewProc("MFCreateAttributes"), ptr(&attrs), 2)
	if e = hr(r); e != nil {
		status.Set(e.Error())
		return
	}
	defer release(attrs)
	_, _ = com(attrs, 21, ptr(guid("{fb394f3d-ccf1-42ee-bbb3-f9b845d5681d}")), 1)
	_, _ = com(attrs, 21, ptr(guid("{9c27891a-ed7a-40e1-88e8-b22727a024ee}")), 1)
	r, _, _ = invoke(mfRead.NewProc("MFCreateSourceReaderFromMediaSource"), source, attrs, ptr(&reader))
	if e = hr(r); e != nil {
		status.Set(e.Error())
		return
	}
	defer release(reader)
	wantedW, wantedH := 1280, 720
	_, _ = fmt.Sscanf(c.Str("resolution"), "%dx%d", &wantedW, &wantedH)
	wantedFPS := c.Num("fps")
	best := math.Inf(1)
	var chosen uintptr
	var width, height int
	var actualFPS float64
	isMJPG := false
	const stream = uintptr(0xfffffffc)
	for i := 0; i < 500; i++ {
		var typ uintptr
		if _, err := com(reader, 5, stream, uintptr(i), ptr(&typ)); err != nil {
			break
		}
		var sub [16]byte
		var size, rate uint64
		_, _ = com(typ, 10, ptr(subType), ptr(&sub))
		_, _ = com(typ, 8, ptr(frameSize), ptr(&size))
		_, _ = com(typ, 8, ptr(frameRate), ptr(&rate))
		w, h := int(size>>32), int(uint32(size))
		fps := float64(rate>>32) / float64(max(1, uint32(rate)))
		nativeMJPG := sub == *(*[16]byte)(unsafe.Pointer(mjpgGUID))
		score := math.Abs(float64(w-wantedW)) + math.Abs(float64(h-wantedH)) + math.Abs(fps-wantedFPS)*20
		preferred := nativeMJPG
		if c.Str("codec") == "YUY2" {
			preferred = sub == *(*[16]byte)(unsafe.Pointer(guid("{32595559-0000-0010-8000-00aa00389b71}")))
		}
		if !preferred {
			score += 100000
		}
		if score < best {
			release(chosen)
			chosen = typ
			best = score
			width = w
			height = h
			actualFPS = fps
			isMJPG = nativeMJPG
		} else {
			release(typ)
		}
	}
	if chosen == 0 {
		status.Set("Камера не предоставила форматы захвата")
		return
	}
	defer release(chosen)
	if _, e = com(reader, 7, stream, 0, chosen); e != nil {
		status.Set(e.Error())
		return
	}
	var engine *WIC
	isMJPG = isMJPG && c.Str("codec") == "MJPG"
	if !isMJPG {
		// Ask Media Foundation to convert any native camera mode into RGB32.
		var rgb uintptr
		r, _, _ = invoke(mf.NewProc("MFCreateMediaType"), ptr(&rgb))
		if e = hr(r); e != nil {
			status.Set(e.Error())
			return
		}
		defer release(rgb)
		_, _ = com(rgb, 24, ptr(guid("{48eba18e-f8c9-4687-bf11-0a74c9f96a8f}")), ptr(guid("{73646976-0000-0010-8000-00aa00389b71}")))
		_, _ = com(rgb, 24, ptr(subType), ptr(guid("{00000016-0000-0010-8000-00aa00389b71}")))
		if _, e = com(reader, 7, stream, 0, rgb); e != nil {
			status.Set("Нет MJPG / преобразования RGB: " + e.Error())
			return
		}
		engine, e = newWIC()
		if e != nil {
			status.Set(e.Error())
			return
		}
		defer engine.Close()
	}
	count := 0
	stamp := time.Now()
	var previous Values
	for ctx.Err() == nil {
		c = store.Get().Camera
		if previous == nil || previous.Bool("autoexp") != c.Bool("autoexp") || previous.Num("exposure") != c.Num("exposure") || previous.Bool("autofocus") != c.Bool("autofocus") || previous.Num("focus") != c.Num("focus") || previous.Bool("autowb") != c.Bool("autowb") || previous.Num("white") != c.Num("white") {
			applyCameraControls(source, c)
			previous = c
		}
		var sample uintptr
		var flags, index uint32
		var timestamp int64
		_, e = com(reader, 9, stream, 0, ptr(&index), ptr(&flags), ptr(&timestamp), ptr(&sample))
		if e != nil {
			status.Set("Камера: " + e.Error())
			return
		}
		if flags&2 != 0 {
			release(sample)
			status.Set("Камера отключена; повторное подключение…")
			return
		}
		if sample == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Millisecond):
			}
			continue
		}
		var buffer uintptr
		_, e = com(sample, 41, ptr(&buffer))
		release(sample)
		if e != nil {
			continue
		}
		var data uintptr
		var maxLen, length uint32
		_, e = com(buffer, 3, ptr(&data), ptr(&maxLen), ptr(&length))
		if e == nil {
			if length > 0 && length < 32<<20 {
				if isMJPG {
					publish(append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(data)), int(length))...))
				} else if int(length) >= width*height*4 {
					jpg, err := engine.EncodeBGRA(data, width, height, 0, c.Int("quality"))
					if err == nil {
						publish(jpg)
					}
				}
			}
			_, _ = com(buffer, 4)
		}
		release(buffer)
		count++
		if time.Since(stamp) >= time.Second {
			status.Set(fmt.Sprintf("Камера %dx%d | %.1f FPS (режим %.1f)", width, height, float64(count)/time.Since(stamp).Seconds(), actualFPS))
			stamp = time.Now()
			count = 0
		}
	}
	_, _ = com(reader, 10, stream)
}
func applyCameraControls(source uintptr, c Values) {
	if camera, e := query(source, guid("{c6e13370-30ac-11d0-a18c-00a0c9118956}")); e == nil {
		defer release(camera)
		for _, x := range []struct {
			property  int
			value     string
			automatic string
		}{{4, "exposure", "autoexp"}, {6, "focus", "autofocus"}} {
			flag := uintptr(2)
			if c.Bool(x.automatic) {
				flag = 1
			}
			_, _ = com(camera, 4, uintptr(x.property), uintptr(int64(c.Int(x.value))), flag)
		}
	}
	if proc, e := query(source, guid("{c6e13360-30ac-11d0-a18c-00a0c9118956}")); e == nil {
		defer release(proc)
		flag := uintptr(2)
		if c.Bool("autowb") {
			flag = 1
		}
		_, _ = com(proc, 4, 7, uintptr(c.Int("white")), flag)
	}
}

type CaptureRect struct {
	X, Y, W, H int
	Name       string
}

type enumerationResult struct {
	monitors []CaptureRect
	windows  []NamedDevice
}

// NewCallback consumes a process-wide slot that Go never frees. These native
// entry points must be registered once, not once per frame or device refresh.
// Opaque request IDs keep concurrent enumerations isolated without passing Go
// slice headers through Windows or retaining completed result buffers.
var enumerationRequests sync.Map
var enumerationID atomic.Uint64
var monitorCallback = syscall.NewCallback(collectMonitor)
var windowCallback = syscall.NewCallback(collectWindow)

func collectMonitor(h, dc, rc, param uintptr) uintptr {
	if state, ok := enumerationRequests.Load(param); ok {
		result := state.(*enumerationResult)
		r := *(*win.RECT)(unsafe.Pointer(rc))
		result.monitors = append(result.monitors, CaptureRect{int(r.Left), int(r.Top), int(r.Right - r.Left), int(r.Bottom - r.Top), fmt.Sprintf("%d: %dx%d (%d,%d)", len(result.monitors)+1, r.Right-r.Left, r.Bottom-r.Top, r.Left, r.Top)})
	}
	return 1
}

func collectWindow(h, param uintptr) uintptr {
	if state, ok := enumerationRequests.Load(param); ok {
		result := state.(*enumerationResult)
		hwnd := win.HWND(h)
		if win.IsWindowVisible(hwnd) {
			nRaw, _, _ := invoke(syscall.NewLazyDLL("user32.dll").NewProc("GetWindowTextLengthW"), h)
			n := int(nRaw)
			if n > 0 {
				buf := make([]uint16, n+1)
				invoke(syscall.NewLazyDLL("user32.dll").NewProc("GetWindowTextW"), h, ptr(&buf[0]), uintptr(len(buf)))
				result.windows = append(result.windows, NamedDevice{strconv.FormatUint(uint64(h), 10), syscall.UTF16ToString(buf)})
			}
		}
	}
	return 1
}

func monitors() []CaptureRect {
	result := &enumerationResult{}
	id := uintptr(enumerationID.Add(1))
	enumerationRequests.Store(id, result)
	defer enumerationRequests.Delete(id)
	invoke(syscall.NewLazyDLL("user32.dll").NewProc("EnumDisplayMonitors"), 0, 0, monitorCallback, id)
	return result.monitors
}

func windowDevices() []NamedDevice {
	result := &enumerationResult{}
	id := uintptr(enumerationID.Add(1))
	enumerationRequests.Store(id, result)
	defer enumerationRequests.Delete(id)
	invoke(syscall.NewLazyDLL("user32.dll").NewProc("EnumWindows"), windowCallback, id)
	return result.windows
}
func screenRect(c Values) (CaptureRect, error) {
	ms := monitors()
	if len(ms) == 0 {
		return CaptureRect{}, fmt.Errorf("мониторы не найдены")
	}
	r := ms[0]
	switch c.Str("capture_kind") {
	case "Область":
		r = CaptureRect{c.Int("left"), c.Int("top"), c.Int("width"), c.Int("height"), "Область"}
	case "Все мониторы":
		r.X = int(win.GetSystemMetrics(win.SM_XVIRTUALSCREEN))
		r.Y = int(win.GetSystemMetrics(win.SM_YVIRTUALSCREEN))
		r.W = int(win.GetSystemMetrics(win.SM_CXVIRTUALSCREEN))
		r.H = int(win.GetSystemMetrics(win.SM_CYVIRTUALSCREEN))
	case "Окно (видимая область)":
		id, _ := strconv.ParseUint(strings.Split(c.Str("window"), ":")[0], 10, 64)
		var wr win.RECT
		if id == 0 || !win.GetWindowRect(win.HWND(id), &wr) || win.IsIconic(win.HWND(id)) {
			return r, fmt.Errorf("выбранное окно закрыто или свёрнуто")
		}
		r = CaptureRect{int(wr.Left), int(wr.Top), int(wr.Right - wr.Left), int(wr.Bottom - wr.Top), "Окно"}
	default:
		i, _ := strconv.Atoi(strings.Split(c.Str("monitor"), ":")[0])
		if i < 1 || i > len(ms) {
			i = 1
		}
		r = ms[i-1]
	}
	if r.W < 1 || r.H < 1 || r.W > 16384 || r.H > 16384 {
		return r, fmt.Errorf("неверная область захвата")
	}
	return r, nil
}
func captureScreen(ctx context.Context, store *ConfigStore, publish func([]byte), status *Status) {
	done, e := comThread()
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer done()
	engine, e := newWIC()
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer engine.Close()
	desktop := win.GetDC(0)
	defer win.ReleaseDC(0, desktop)
	dc := win.CreateCompatibleDC(desktop)
	defer win.DeleteDC(dc)
	var bitmap win.HBITMAP
	var old win.HGDIOBJ
	var bits uintptr
	lastW, lastH := 0, 0
	defer func() {
		if old != 0 {
			win.SelectObject(dc, old)
		}
		if bitmap != 0 {
			win.DeleteObject(win.HGDIOBJ(bitmap))
		}
	}()
	count := 0
	stamp := time.Now()
	for ctx.Err() == nil {
		begin := time.Now()
		c := store.Get().Screen
		if c.Bool("pause_video") {
			status.Set("Экран на паузе")
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		r, err := screenRect(c)
		if err != nil {
			status.Set(err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if r.W != lastW || r.H != lastH {
			if old != 0 {
				win.SelectObject(dc, old)
			}
			if bitmap != 0 {
				win.DeleteObject(win.HGDIOBJ(bitmap))
			}
			info := win.BITMAPINFO{BmiHeader: win.BITMAPINFOHEADER{BiSize: 40, BiWidth: int32(r.W), BiHeight: -int32(r.H), BiPlanes: 1, BiBitCount: 32}}
			var memory unsafe.Pointer
			bitmap = win.CreateDIBSection(desktop, &info.BmiHeader, win.DIB_RGB_COLORS, &memory, 0, 0)
			bits = uintptr(memory)
			if bitmap == 0 {
				status.Set("Ошибка выделения изображения экрана")
				return
			}
			old = win.SelectObject(dc, win.HGDIOBJ(bitmap))
			lastW, lastH = r.W, r.H
		}
		if !win.BitBlt(dc, 0, 0, int32(r.W), int32(r.H), desktop, int32(r.X), int32(r.Y), win.SRCCOPY|win.CAPTUREBLT) {
			status.Set("Ошибка захвата экрана")
			return
		}
		if c.Bool("cursor") {
			drawCursor(dc, r)
		}
		jpg, err := engine.EncodeBGRA(bits, r.W, r.H, c.Int("max_width"), c.Int("quality"))
		if err != nil {
			status.Set("JPEG: " + err.Error())
			return
		}
		publish(jpg)
		count++
		if time.Since(stamp) >= time.Second {
			status.Set(fmt.Sprintf("Экран %dx%d | %.1f FPS", r.W, r.H, float64(count)/time.Since(stamp).Seconds()))
			count = 0
			stamp = time.Now()
		}
		delay := time.Second/time.Duration(max(1, c.Int("fps"))) - time.Since(begin)
		if delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}
	runtime.KeepAlive(bits)
}
func drawCursor(dc win.HDC, r CaptureRect) {
	var ci struct {
		CbSize, Flags uint32
		HCursor       win.HCURSOR
		PtScreenPos   win.POINT
	}
	ci.CbSize = uint32(unsafe.Sizeof(ci))
	ok, _, _ := invoke(syscall.NewLazyDLL("user32.dll").NewProc("GetCursorInfo"), ptr(&ci))
	if ok == 0 || ci.Flags&1 == 0 {
		return
	}
	var ii win.ICONINFO
	if !win.GetIconInfo(win.HICON(ci.HCursor), &ii) {
		return
	}
	defer func() {
		if ii.HbmMask != 0 {
			win.DeleteObject(win.HGDIOBJ(ii.HbmMask))
		}
		if ii.HbmColor != 0 {
			win.DeleteObject(win.HGDIOBJ(ii.HbmColor))
		}
	}()
	win.DrawIconEx(dc, ci.PtScreenPos.X-int32(ii.XHotspot)-int32(r.X), ci.PtScreenPos.Y-int32(ii.YHotspot)-int32(r.Y), win.HICON(ci.HCursor), 0, 0, 0, 0, win.DI_NORMAL)
}
