package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/lxn/win"
)

var mf = syscall.NewLazyDLL("mfplat.dll")
var mfDevice = syscall.NewLazyDLL("mf.dll")
var mfRead = syscall.NewLazyDLL("mfreadwrite.dll")
var devType = guid("{c60ac5fe-252a-478f-a0ef-bc8fa5f7cad3}")
var videoType = guid("{8ac3587a-4ae7-42d8-99e0-0a6013eef90f}")
var friendly = guid("{60d0e559-52f8-4fa2-bbce-acdb34a8ec01}")
var cameraSymbolicLink = guid("{58f0aad8-22bf-4f8a-bb3d-d2c4978c6e2f}")
var subType = guid("{f7e34c9a-42e8-4714-b74b-cb29d72c35e5}")
var frameSize = guid("{1652c33d-d6b2-4012-b834-72030849a37d}")
var frameRate = guid("{c459a2e8-3d2c-4e44-b132-fee5156c7bb0}")
var frameRateMin = guid("{d2e7558c-dc1f-403f-9a72-d28bb1eb3b5e}")
var frameRateMax = guid("{e3371d41-b4cf-4a05-bd4e-20b88bb2c4d6}")
var iidMediaSource = guid("{279a808d-aec7-40c8-9c6b-a6b492c78a66}")
var mjpgGUID = guid("{47504a4d-0000-0010-8000-00aa00389b71}")

type NamedDevice struct{ ID, Name string }

var mfPlatform struct {
	sync.Mutex
	users int
}

var cameraOwners sync.Map

func retryCamera(ctx context.Context, store *ConfigStore, publish func([]byte), status *Status, budget func() int) {
	wait := time.Second
	for ctx.Err() == nil {
		delivered := false
		captureCamera(ctx, store, func(frame []byte) {
			delivered = true
			publish(frame)
		}, status, budget)
		if ctx.Err() != nil {
			return
		}
		if delivered {
			wait = time.Second
		}
		timer := time.NewTimer(wait + time.Duration(rand.IntN(450))*time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		wait = min(2*wait, 8*time.Second)
	}
}

func cameraRate(rate uint64) float64 {
	return float64(rate>>32) / float64(max(1, uint32(rate)))
}

// mfStart добавляет к COM-потоку время жизни Media Foundation. defer done
// должен выполняться после освобождения reader/source и прочих MF-объектов.
func mfStart() (func(), error) {
	done, e := comThread()
	if e != nil {
		return nil, e
	}
	mfPlatform.Lock()
	if mfPlatform.users == 0 {
		r, _, _ := invoke(mf.NewProc("MFStartup"), 0x20070, 0)
		if e = hr(r); e != nil {
			mfPlatform.Unlock()
			done()
			return nil, e
		}
	}
	mfPlatform.users++
	mfPlatform.Unlock()
	return func() {
		mfPlatform.Lock()
		mfPlatform.users--
		if mfPlatform.users == 0 {
			invoke(mf.NewProc("MFShutdown"))
		}
		mfPlatform.Unlock()
		done()
	}, nil
}

// mfDevices возвращает ссылки IMFActivate, которыми владеет вызывающий код.
// Массив указателей освобождается CoTaskMemFree отдельно от Release каждого элемента.
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

func cameraAttribute(device uintptr, key *ole.GUID) string {
	var data *uint16
	var length uint32
	if _, err := com(device, 13, ptr(key), ptr(&data), ptr(&length)); err != nil || data == nil {
		return ""
	}
	defer invoke(oleDLL.NewProc("CoTaskMemFree"), uintptr(unsafe.Pointer(data)))
	return syscall.UTF16ToString(unsafe.Slice(data, int(length)))
}

// Prefer Media Foundation symbolic links so enumeration order does not swap feeds.
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
	result := describeCameras(devices)
	for _, d := range devices {
		release(d)
	}
	return result, nil
}

func describeCameras(devices []uintptr) []NamedDevice {
	result := make([]NamedDevice, 0, len(devices))
	for i, d := range devices {
		name := cameraAttribute(d, friendly)
		if name == "" {
			name = fmt.Sprintf("Камера %d", i+1)
		}
		id := cameraAttribute(d, cameraSymbolicLink)
		if id == "" {
			id = strconv.Itoa(i)
		}
		result = append(result, NamedDevice{id, name})
	}
	return result
}

// captureCamera создаёт MF reader на собственном COM-потоке и выбирает ближайший
// нативный режим с предпочтением codec. В MJPG передаёт копию JPEG, иначе просит
// RGB32 и кодирует WIC. Асинхронный reader допускает отмену и тайм-аут кадра.
func captureCamera(ctx context.Context, store *ConfigStore, publish func([]byte), status *Status, budget func() int) {
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
	available := describeCameras(devices)
	id, index := cameraDeviceSelection(available, c.Str("camera"))
	if index < 0 {
		status.Set("Выбранная камера недоступна")
		return
	}
	key := strings.ToLower(id)
	token, _ := cameraOwners.LoadOrStore(key, make(chan struct{}, 1))
	gate := token.(chan struct{})
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return
	}
	if ctx.Err() != nil {
		return
	}
	name := available[index].Name
	var source uintptr
	if _, e = com(devices[index], 33, ptr(iidMediaSource), ptr(&source)); e != nil {
		status.Set(e.Error())
		return
	}
	defer release(source)
	defer func() { _, _ = com(source, 12) }() // IMFMediaSource.Shutdown
	var reader, attrs uintptr
	r, _, _ := invoke(mf.NewProc("MFCreateAttributes"), ptr(&attrs), 4)
	if e = hr(r); e != nil {
		status.Set(e.Error())
		return
	}
	defer release(attrs)
	callbackHandle, callback, e := newCameraReaderCallback()
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer release(callbackHandle)
	if _, e = com(attrs, 27, ptr(readerAsyncCallback), callbackHandle); e == nil { // IMFAttributes.SetUnknown
		_, e = com(attrs, 21, ptr(readerLeavesSourceOpen), 1)
	}
	if e != nil {
		status.Set("Настройка Source Reader: " + e.Error())
		return
	}
	_, _ = com(attrs, 21, ptr(guid("{fb394f3d-ccf1-42ee-bbb3-f9b845d5681d}")), 1)
	_, _ = com(attrs, 21, ptr(guid("{9c27891a-ed7a-40e1-88e8-b22727a024ee}")), 1)
	r, _, _ = invoke(mfRead.NewProc("MFCreateSourceReaderFromMediaSource"), source, attrs, ptr(&reader))
	if e = hr(r); e != nil {
		status.Set(e.Error())
		return
	}
	defer release(reader)
	defer callback.stop()
	wantedW, wantedH := 1280, 720
	_, _ = fmt.Sscanf(c.Str("resolution"), "%dx%d", &wantedW, &wantedH)
	wantedFPS := c.Num("fps")
	best := math.Inf(1)
	var chosen uintptr
	var width, height int
	var actualFPS, chosenFPS float64
	var nativeRate uint64
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
		fps := cameraRate(rate)
		var minRate, maxRate uint64
		_, minErr := com(typ, 8, ptr(frameRateMin), ptr(&minRate))
		_, maxErr := com(typ, 8, ptr(frameRateMax), ptr(&maxRate))
		targetFPS := fps
		if minErr == nil && maxErr == nil && cameraRate(minRate) > 0 && cameraRate(maxRate) >= cameraRate(minRate) {
			targetFPS = max(cameraRate(minRate), min(wantedFPS, cameraRate(maxRate)))
		}
		nativeMJPG := sub == *(*[16]byte)(unsafe.Pointer(mjpgGUID))
		score := math.Abs(float64(w-wantedW)) + math.Abs(float64(h-wantedH)) + math.Abs(targetFPS-wantedFPS)*20
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
			actualFPS, chosenFPS, nativeRate = fps, targetFPS, rate
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
	requestedRate := false
	if chosenFPS > 0 && chosenFPS != actualFPS {
		rate := uint64(math.Round(chosenFPS*1000))<<32 | 1000
		_, err := com(chosen, 22, ptr(frameRate), rate)
		requestedRate = err == nil
	}
	_, e = com(reader, 7, stream, 0, chosen)
	if requestedRate {
		if e == nil {
			actualFPS = chosenFPS
		} else if _, restoreErr := com(chosen, 22, ptr(frameRate), nativeRate); restoreErr == nil {
			_, e = com(reader, 7, stream, 0, chosen)
		}
	}
	if e != nil {
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
	credit := 0.0
	publishRatio := min(1, wantedFPS/max(wantedFPS, actualFPS))
	var previous Values
	deadline := time.NewTimer(time.Hour)
	deadline.Stop()
	log.Printf("CAMERA READER name=%q size=%dx%d native_fps=%.1f requested_fps=%.1f mjpg=%t", name, width, height, actualFPS, wantedFPS, isMJPG)
	for ctx.Err() == nil {
		c = store.Get().Camera
		if previous == nil || previous.Bool("autoexp") != c.Bool("autoexp") || previous.Num("exposure") != c.Num("exposure") || previous.Bool("autofocus") != c.Bool("autofocus") || previous.Num("focus") != c.Num("focus") || previous.Bool("autowb") != c.Bool("autowb") || previous.Num("white") != c.Num("white") {
			applyCameraControls(source, c)
			previous = c
		}
		result, e := callback.read(ctx, reader, stream, deadline)
		sample := result.sample
		if e != nil {
			release(sample)
			if ctx.Err() == nil {
				log.Printf("CAMERA READ FAILED name=%q: %v", name, e)
				status.Set("Камера: " + e.Error() + " · повторное подключение")
			}
			return
		}
		if result.flags&3 != 0 { // MF_SOURCE_READERF_ERROR or ENDOFSTREAM
			release(sample)
			log.Printf("CAMERA SOURCE ENDED name=%q flags=0x%x", name, result.flags)
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
		level := 0
		if budget != nil {
			level = budget()
		}
		// Budget applies to the frames the driver actually produces. Skip before
		// locking the sample buffer, and keep native MJPG on the passthrough path.
		credit += publishRatio * float64(adaptiveFPS[level])
		if credit < 100 {
			release(sample)
			continue
		}
		credit -= 100
		frameWidth := width
		var buffer uintptr
		_, e = com(sample, 41, ptr(&buffer))
		if e != nil {
			release(sample)
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
					limit, quality := budgetJPEG(level, width, 0, c.Int("quality"), c.Bool("adaptive_resolution"))
					jpg, err := engine.EncodeBGRA(data, width, height, limit, quality)
					if err == nil {
						if limit > 0 {
							frameWidth = min(width, limit)
						}
						publish(jpg)
					}
				}
			}
			_, _ = com(buffer, 4)
		}
		release(buffer)
		// Keep the sample alive through the copy and unlock. A camera allocator
		// may recycle sample-backed storage once its sample is released.
		release(sample)
		count++
		if time.Since(stamp) >= time.Second {
			status.Set(fmt.Sprintf("Камера %dx%d | %.1f FPS (режим %.1f)", frameWidth, max(1, height*frameWidth/width), float64(count)/time.Since(stamp).Seconds(), actualFPS))
			stamp = time.Now()
			count = 0
		}
	}
}

// applyCameraControls пробует старые интерфейсы управления как best effort.
// MF source может их не предоставлять; ошибки не означают применённую настройку.
// Поэтому аппаратная группа в Go-панели скрыта.
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

// CaptureRect использует физические пиксели виртуального рабочего стола.
// X/Y могут быть отрицательными для монитора левее/выше основного.
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

// collectMonitor — постоянный native entry point. param — ID в Go registry,
// а rc — заимствованный RECT Windows, доступный только во время callback.
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

// monitors регистрирует только короткоживущий контекст результата.
// Go удерживает регистрации callback до конца процесса, поэтому в покадровом
// пути переиспользуется постоянный monitorCallback вместо нового NewCallback.
func monitors() []CaptureRect {
	result := &enumerationResult{}
	id := uintptr(enumerationID.Add(1))
	enumerationRequests.Store(id, result)
	defer enumerationRequests.Delete(id)
	invoke(syscall.NewLazyDLL("user32.dll").NewProc("EnumDisplayMonitors"), 0, 0, monitorCallback, id)
	return result.monitors
}

// windowDevices использует тот же принцип постоянного callback и отдельного ID.
// HWND живёт только пока существует окно; сохранённое число нужно перепроверять.
func windowDevices() []NamedDevice {
	result := &enumerationResult{}
	id := uintptr(enumerationID.Add(1))
	enumerationRequests.Store(id, result)
	defer enumerationRequests.Delete(id)
	invoke(syscall.NewLazyDLL("user32.dll").NewProc("EnumWindows"), windowCallback, id)
	return result.windows
}

// screenRect разрешает выбранный источник в текущие координаты рабочего стола.
// Захват окна означает видимые пиксели его прямоугольника, включая перекрытия.
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

// captureScreen владеет desktop DC, совместимым DC и top-down DIB.
// DIB пересоздаётся только при изменении размеров; перед DeleteObject старый bitmap
// обязательно выводится из DC. Каждая итерация кодирует JPEG и учитывает время работы.
func captureScreen(ctx context.Context, store *ConfigStore, publish func([]byte), status *Status, budget func() int) {
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
		level := 0
		if budget != nil {
			level = budget()
		}
		width, quality := budgetJPEG(level, r.W, c.Int("max_width"), c.Int("quality"), c.Bool("adaptive_resolution"))
		jpg, err := engine.EncodeBGRA(bits, r.W, r.H, width, quality)
		if err != nil {
			status.Set("JPEG: " + err.Error())
			return
		}
		publish(jpg)
		count++
		if time.Since(stamp) >= time.Second {
			w, h := r.W, r.H
			if width > 0 && width < w {
				w, h = width, max(1, r.H*width/r.W)
			}
			status.Set(fmt.Sprintf("Экран %dx%d | %.1f FPS", w, h, float64(count)/time.Since(stamp).Seconds()))
			count = 0
			stamp = time.Now()
		}
		delay := time.Second/time.Duration(budgetFPS(level, c.Int("fps"))) - time.Since(begin)
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

// drawCursor переводит экранную позицию курсора в координаты области с hotspot.
// GetIconInfo создаёт bitmap handles, которые нужно DeleteObject после рисования.
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
