package main

import (
	"context"
	"fmt"
	"github.com/bukirev/webcam-low-latency/go-version/internal/brand"
	"github.com/gonutz/d3d9"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Shader обрабатывает уже декодированную текстуру. Порядок gamma → contrast/
// brightness → saturation → clamp определяет итоговый цвет; резкость берёт соседей.
// Настройки изменяют только локальный вид, а не передаваемый JPEG.
const shaderSource = `sampler2D picture : register(s0);
float4 adjust : register(c0);
float4 detail : register(c1);
float4 main(float2 uv:TEXCOORD0):COLOR0 {
 float3 color=tex2D(picture,uv).rgb;
 if(detail.x>0) { float3 blur=(tex2D(picture,uv+float2(detail.y,0)).rgb+tex2D(picture,uv-float2(detail.y,0)).rgb+tex2D(picture,uv+float2(0,detail.z)).rgb+tex2D(picture,uv-float2(0,detail.z)).rgb)*0.25; color=color+(color-blur)*detail.x; }
 color=pow(saturate(color),adjust.w);
 color=(color-0.5)*adjust.y+0.5+adjust.x;
 float gray=dot(color,float3(0.299,0.587,0.114));
 color=lerp(float3(gray,gray,gray),color,adjust.z);
 return float4(saturate(color),1);
}`

var shaderOnce sync.Once
var shaderCode []byte
var shaderError error

// compileShader кеширует bytecode через sync.Once; ошибки компиляции также кешируются.
// ID3DBlob::GetBufferPointer/GetBufferSize возвращают значения, а не HRESULT.
func compileShader() ([]byte, error) {
	shaderOnce.Do(func() {
		dll := syscall.NewLazyDLL("d3dcompiler_47.dll")
		proc := dll.NewProc("D3DCompile")
		if e := proc.Find(); e != nil {
			shaderError = e
			return
		}
		src := []byte(shaderSource)
		main, _ := syscall.BytePtrFromString("main")
		target, _ := syscall.BytePtrFromString("ps_3_0")
		var code, errors uintptr
		r, _, _ := invoke(proc, ptr(&src[0]), uintptr(len(src)), 0, 0, 0, ptr(main), ptr(target), 0x8000, 0, ptr(&code), ptr(&errors))
		defer release(code)
		defer release(errors)
		if e := hr(r); e != nil {
			shaderError = e
			if errors != 0 {
				p, _ := com(errors, 3)
				size, _ := com(errors, 4)
				shaderError = fmt.Errorf("шейдер: %s", string(unsafe.Slice((*byte)(unsafe.Pointer(p)), int(size))))
			}
			return
		}
		p, _ := com(code, 3)
		size, _ := com(code, 4)
		shaderCode = append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(p)), int(size))...)
	})
	return shaderCode, shaderError
}

// GPU принадлежит UI OS thread. Методы не защищены собственным mutex.
// generation относится к Sequence кадра: повторная WM_PAINT не должна копировать
// ту же BGRA-картинку в видеопамять заново.
type GPU struct {
	d3d                   *d3d9.Direct3D
	device                *d3d9.Device
	texture               *d3d9.Texture
	shader                *d3d9.PixelShader
	width, height, tw, th int
	generation            uint64
	reading               bool
}

func (g *GPU) Close() {
	if g.texture != nil {
		g.texture.Release()
		g.texture = nil
	}
	if g.shader != nil {
		g.shader.Release()
		g.shader = nil
	}
	if g.device != nil {
		g.device.Release()
		g.device = nil
	}
	if g.d3d != nil {
		g.d3d.Release()
		g.d3d = nil
	}
}

// Initialize создаёт windowed HAL device с немедленным Present.
// SOFTWARE_VERTEXPROCESSING касается четырёх вершин, pixel shader работает на GPU.
// При изменении размеров реализация пересоздаёт device вместо сложного Reset.
func (g *GPU) Initialize(hwnd win.HWND, w, h int) error {

	g.Close()
	var e error
	g.d3d, e = d3d9.Create(d3d9.SDK_VERSION)
	if e != nil {
		return e
	}
	p := d3d9.PRESENT_PARAMETERS{BackBufferWidth: uint32(w), BackBufferHeight: uint32(h), BackBufferFormat: d3d9.FMT_UNKNOWN, BackBufferCount: 1, SwapEffect: d3d9.SWAPEFFECT_DISCARD, HDeviceWindow: d3d9.HWND(hwnd), Windowed: 1, PresentationInterval: d3d9.PRESENT_INTERVAL_IMMEDIATE}

	g.device, _, e = g.d3d.CreateDevice(0, d3d9.DEVTYPE_HAL, d3d9.HWND(hwnd), d3d9.CREATE_SOFTWARE_VERTEXPROCESSING|d3d9.CREATE_FPU_PRESERVE, p)
	if e != nil {
		return e
	}

	code, e := compileShader()
	if e != nil {
		return e
	}

	g.shader, e = g.device.CreatePixelShaderFromBytes(code)
	if e != nil {
		return e
	}
	g.width, g.height = w, h
	g.generation = 0
	_ = g.device.SetRenderState(d3d9.RS_LIGHTING, 0)
	_ = g.device.SetRenderState(d3d9.RS_CULLMODE, uint32(d3d9.CULL_NONE))
	_ = g.device.SetRenderState(d3d9.RS_ZENABLE, 0)
	_ = g.device.SetSamplerState(0, d3d9.SAMP_MINFILTER, uint32(d3d9.TEXF_LINEAR))
	_ = g.device.SetSamplerState(0, d3d9.SAMP_MAGFILTER, uint32(d3d9.TEXF_LINEAR))
	_ = g.device.SetSamplerState(0, d3d9.SAMP_ADDRESSU, uint32(d3d9.TADDRESS_CLAMP))
	_ = g.device.SetSamplerState(0, d3d9.SAMP_ADDRESSV, uint32(d3d9.TADDRESS_CLAMP))
	return nil
}

type vertex struct{ X, Y, Z, RHW, U, V float32 }

// imageQuad меняет геометрию и UV четырёх вершин, не вращая/масштабируя CPU-буфер.
// Полпикселя компенсирует координаты rasterizer D3D9; rotation влияет на aspect.
func imageQuad(fw, fh, ww, wh int, c Values) []vertex {
	rw, rh := float64(fw), float64(fh)
	rotation := c.Int("rotation")
	if rotation == 90 || rotation == 270 {
		rw, rh = rh, rw
	}
	sourceAspect := rw / rh
	aspect := sourceAspect
	if ratio := c.Str("ratio"); ratio != "<nil>" && ratio != "" && ratio != "Оригинал" {
		var a, b float64
		if _, e := fmt.Sscanf(ratio, "%f:%f", &a, &b); e == nil && b > 0 {
			aspect = a / b
		}
	}
	targetW, targetH := float64(ww), float64(wh)
	if targetW/targetH > aspect {
		targetW = targetH * aspect
	} else {
		targetH = targetW / aspect
	}
	uSize, vSize := 1., 1.
	fit := c.Str("fit")
	if fit == "Вписать" {
		if targetW/targetH > sourceAspect {
			targetW = targetH * sourceAspect
		} else {
			targetH = targetW / sourceAspect
		}
	} else if fit == "Заполнить" {
		if sourceAspect > targetW/targetH {
			uSize = (targetW / targetH) / sourceAspect
		} else {
			vSize = sourceAspect / (targetW / targetH)
		}
	}
	z := max(1., c.Num("zoom"))
	uSize /= z
	vSize /= z
	x, y := (float64(ww)-targetW)/2-.5, (float64(wh)-targetH)/2-.5
	positions := [][4]float64{{x, y, 0, 0}, {x + targetW, y, 1, 0}, {x, y + targetH, 0, 1}, {x + targetW, y + targetH, 1, 1}}
	out := make([]vertex, 4)
	for i, p := range positions {
		u, v := p[2], p[3]
		if c.Bool("mirror") {
			u = 1 - u
		}
		if c.Bool("flip") {
			v = 1 - v
		}
		u = .5 + (u-.5)*uSize
		v = .5 + (v-.5)*vSize
		switch rotation {
		case 90:
			u, v = v, 1-u
		case 180:
			u, v = 1-u, 1-v
		case 270:
			u, v = 1-v, u
		}
		out[i] = vertex{float32(p[0]), float32(p[1]), 0, 1, float32(u), float32(v)}
	}
	return out
}

// Paint загружает новую текстуру построчно с учётом native Pitch.
// Во время reading рисование заканчивается до Present: back buffer нужен снимку.
func (g *GPU) Paint(hwnd win.HWND, w, h int, frame *PixelFrame, c Values) error {
	if w < 1 || h < 1 {
		return nil
	}
	if g.device == nil || g.width != w || g.height != h {
		if e := g.Initialize(hwnd, w, h); e != nil {
			return e
		}
	}
	if e := g.device.Clear(nil, d3d9.CLEAR_TARGET, 0xff101318, 1, 0); e != nil {
		g.Close()
		return e
	}
	if frame != nil {
		if g.texture == nil || g.tw != frame.Width || g.th != frame.Height {
			if g.texture != nil {
				g.texture.Release()
			}
			var e error
			g.texture, e = g.device.CreateTexture(uint(frame.Width), uint(frame.Height), 1, d3d9.USAGE_DYNAMIC, d3d9.FMT_A8R8G8B8, d3d9.POOL_DEFAULT, 0)
			if e != nil {
				return e
			}
			g.tw, g.th = frame.Width, frame.Height
			g.generation = 0
		}
		if g.generation != frame.Sequence {
			lock, e := g.texture.LockRect(0, nil, d3d9.LOCK_DISCARD)
			if e != nil {
				return e
			}
			for y := 0; y < frame.Height; y++ {
				dst := unsafe.Slice((*byte)(unsafe.Pointer(lock.PBits+uintptr(y)*uintptr(lock.Pitch))), frame.Width*4)
				copy(dst, frame.Pixels[y*frame.Width*4:(y+1)*frame.Width*4])
			}
			if e = g.texture.UnlockRect(0); e != nil {
				return e
			}
			g.generation = frame.Sequence
		}
		_ = g.device.SetTexture(0, g.texture)
		_ = g.device.SetPixelShader(g.shader)
		_ = g.device.SetPixelShaderConstantF(0, []float32{float32(c.Num("brightness") / 255), float32(c.Num("contrast")), float32(c.Num("saturation")), 1 / float32(max(.2, c.Num("gamma")))})
		_ = g.device.SetPixelShaderConstantF(1, []float32{float32(c.Num("sharpness")), 1 / float32(frame.Width), 1 / float32(frame.Height), 0})
		_ = g.device.SetFVF(d3d9.FVF_XYZRHW | d3d9.FVF_TEX1)
		vertices := imageQuad(frame.Width, frame.Height, w, h, c)
		if e := g.device.BeginScene(); e != nil {
			return e
		}
		e := g.device.DrawPrimitiveUP(d3d9.PT_TRIANGLESTRIP, 2, uintptr(ptr(&vertices[0])), uint(unsafe.Sizeof(vertex{})))
		_ = g.device.EndScene()
		runtime.KeepAlive(vertices)
		if e != nil {
			return e
		}
	}

	if g.reading {
		return nil
	}
	if e := g.device.Present(nil, nil, 0, nil); e != nil {
		g.Close()
		return e
	}
	return nil
}

// Capture the rendered back buffer so snapshots include every GPU adjustment.
func (g *GPU) readback() (*PixelFrame, error) {
	back, e := g.device.GetBackBuffer(0, 0, d3d9.BACKBUFFER_TYPE_MONO)
	if e != nil {
		return nil, e
	}
	defer back.Release()
	desc, e := back.GetDesc()
	if e != nil {
		return nil, e
	}
	surface, e := g.device.CreateOffscreenPlainSurface(uint(desc.Width), uint(desc.Height), desc.Format, d3d9.POOL_SYSTEMMEM, 0)
	if e != nil {
		return nil, e
	}
	defer surface.Release()
	if e := g.device.GetRenderTargetData(back, surface); e != nil {
		return nil, e
	}
	locked, e := surface.LockRect(nil, d3d9.LOCK_READONLY)
	if e != nil {
		return nil, e
	}
	defer surface.UnlockRect()
	f := &PixelFrame{Width: int(desc.Width), Height: int(desc.Height), Pixels: make([]byte, int(desc.Width)*int(desc.Height)*4)}
	for y := 0; y < f.Height; y++ {
		copy(f.Pixels[y*f.Width*4:(y+1)*f.Width*4], unsafe.Slice((*byte)(unsafe.Pointer(locked.PBits+uintptr(y)*uintptr(locked.Pitch))), f.Width*4))
	}
	return f, nil
}

// Viewer связывает UI и decoder: frame защищён mu, closed — atomic.
// HWND фиксируются при создании; worker не должен вызывать методы Walk-виджетов.
// InvalidateRect лишь запрашивает объединяемую WM_PAINT, не ставит кадры в FIFO.
type Viewer struct {
	window           *walk.MainWindow
	canvas           *walk.CustomWidget
	mu               sync.Mutex
	frame            *PixelFrame
	gpu              GPU
	store            *ConfigStore
	kind             string
	sequence         uint64
	status           *Status
	closed           atomic.Bool
	senderPreview    bool
	previewShown     atomic.Bool
	disposing        bool
	hwnd, canvasHWND win.HWND
	onClose          func()
}

func newViewer(kind string, store *ConfigStore, onClose func()) (*Viewer, error) {
	v := &Viewer{kind: kind, store: store, status: &Status{}, onClose: onClose, senderPreview: store.Get().Role == "server"}
	title := "Камера"
	if kind == "screen" {
		title = "Экран и звук"
	}
	size := Size{1000, 700}
	var tools []Widget
	if v.senderPreview {
		size = Size{720, 480}
		tools = append(tools, PushButton{Text: "Скрыть", OnClicked: v.Hide})
	}
	tools = append(tools, HSpacer{}, PushButton{Text: "Полный экран (F11)", OnClicked: func() { _ = v.window.SetFullscreen(!v.window.Fullscreen()) }}, PushButton{Text: "Снимок (F5)", OnClicked: func() { v.Snapshot() }})
	e := (MainWindow{AssignTo: &v.window, Title: brand.Name + " — " + title, Size: size, MinSize: Size{320, 240}, Visible: !v.senderPreview, Layout: VBox{MarginsZero: true, SpacingZero: true}, Children: []Widget{
		Composite{Layout: HBox{}, Children: tools},
		CustomWidget{AssignTo: &v.canvas, PaintPixels: func(c *walk.Canvas, b walk.Rectangle) error { return v.paint(c, b) }, PaintMode: PaintNoErase, InvalidatesOnResize: true},
	}}).Create()
	if e != nil {
		return nil, e
	}
	brand.Window(v.window)

	v.hwnd, v.canvasHWND = v.window.Handle(), v.canvas.Handle()
	v.canvas.SetMinMaxSize(walk.Size{Width: 100, Height: 100}, walk.Size{})
	v.window.Closing().Attach(func(cancel *bool, _ walk.CloseReason) {
		if v.senderPreview && !v.disposing {
			*cancel = true
			v.Hide()
			return
		}
		v.previewShown.Store(false)
		v.mu.Lock()
		v.closed.Store(true)
		freePixels(v.frame)
		v.frame = nil
		v.mu.Unlock()
		v.gpu.Close()
		if v.onClose != nil {
			v.onClose()
		}
	})
	v.window.KeyDown().Attach(func(key walk.Key) {
		switch key {
		case walk.KeyF11:
			_ = v.window.SetFullscreen(!v.window.Fullscreen())
		case walk.KeyF5:
			v.Snapshot()
		case walk.KeyEscape:
			if v.window.Fullscreen() {
				_ = v.window.SetFullscreen(false)
			} else if v.senderPreview {
				v.Hide()
			}
		}
	})

	if !v.senderPreview {
		v.window.Show()
	}
	v.ApplyWindowOptions()

	return v, nil
}
func (v *Viewer) values() Values {
	c := v.store.Get()
	if v.kind == "camera" {
		return c.Camera
	}
	return c.Screen
}
func (v *Viewer) ApplyWindowOptions() {
	if v.closed.Load() {
		return
	}
	top := win.HWND_NOTOPMOST
	if v.values().Bool("top") || v.values().Bool("topmost") {
		top = win.HWND_TOPMOST
	}
	win.SetWindowPos(v.hwnd, top, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
	win.InvalidateRect(v.canvasHWND, nil, false)
}
func (v *Viewer) Clear() {
	v.mu.Lock()
	freePixels(v.frame)
	v.frame = nil
	v.mu.Unlock()
	if !v.closed.Load() {
		win.InvalidateRect(v.canvasHWND, nil, false)
	}
}
func (v *Viewer) Visible() bool {
	return !v.closed.Load() && win.IsWindowVisible(v.hwnd) && !win.IsIconic(v.hwnd) && v.previewEnabled()
}

func (v *Viewer) previewEnabled() bool {
	if v.senderPreview {
		return v.previewShown.Load()
	}
	return v.values().Bool("preview")
}

// Publish принимает единоличное владение frame, освобождает прежний под mu
// и инвалидирует canvas. Производитель больше не обращается к переданному Pixels.
func (v *Viewer) Publish(frame *PixelFrame) {
	v.mu.Lock()
	if v.closed.Load() || v.senderPreview && !v.previewShown.Load() {
		v.mu.Unlock()
		freePixels(frame)
		return
	}
	v.sequence++
	frame.Sequence = v.sequence
	old := v.frame
	v.frame = frame
	freePixels(old)
	v.mu.Unlock()
	win.InvalidateRect(v.canvasHWND, nil, false)
}
func (v *Viewer) paint(canvas *walk.Canvas, b walk.Rectangle) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed.Load() {
		return nil
	}
	frame := v.frame
	if !v.previewEnabled() {
		if v.senderPreview {
			return nil
		}
		frame = nil
	}
	e := v.gpu.Paint(v.canvas.Handle(), b.Width, b.Height, frame, v.values())
	if e != nil {
		v.status.Set("Direct3D: " + e.Error())
		return nil
	}
	if frame != nil {
		v.status.Set(fmt.Sprintf("Direct3D GPU | %dx%d", frame.Width, frame.Height))
	}
	return nil
}
func (v *Viewer) Show() {
	if !v.closed.Load() {
		v.previewShown.Store(true)
		v.window.Show()
		v.window.SetFocus()
	}
}
func (v *Viewer) Hide() {
	v.previewShown.Store(false)
	v.window.Hide()
	if v.window.Fullscreen() {
		_ = v.window.SetFullscreen(false)
		v.window.Hide()
	}
	v.Clear()
	v.gpu.Close()
	v.status.Set("")
}
func (v *Viewer) Close() {
	if !v.closed.Load() {
		v.disposing = true
		v.window.Close()
	}
}

// Snapshot читает back buffer на UI-потоке, затем сохраняет независимую копию
// PNG в горутине. Снимок включает обработку, поворот и поля текущего окна.
func (v *Viewer) Snapshot() {
	v.mu.Lock()
	if v.frame == nil {
		v.mu.Unlock()
		return
	}
	bounds := v.canvas.ClientBoundsPixels()
	v.gpu.reading = true
	e := v.gpu.Paint(v.canvasHWND, bounds.Width, bounds.Height, v.frame, v.values())
	v.gpu.reading = false
	var f *PixelFrame
	if e == nil {
		f, e = v.gpu.readback()
	}
	if v.gpu.device != nil {
		_ = v.gpu.device.Present(nil, nil, 0, nil)
	}
	v.mu.Unlock()
	if e != nil {
		v.status.Set("Снимок: " + e.Error())
		return
	}
	go func() {
		img := image.NewRGBA(image.Rect(0, 0, f.Width, f.Height))
		for i := 0; i < len(f.Pixels); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = f.Pixels[i+2], f.Pixels[i+1], f.Pixels[i], 255
		}
		_ = os.MkdirAll("snapshots", 0755)
		name := filepath.Join("snapshots", v.kind+"_"+time.Now().Format("20060102_150405.000000000")+".png")
		out, e := os.Create(name)
		if e == nil {
			e = png.Encode(out, img)
			out.Close()
		}
		if e != nil {
			v.status.Set(e.Error())
		} else {
			v.status.Set("Снимок: " + name)
		}
	}()
}

// decodeVideo живёт на отдельном COM-потоке и берёт только свежий JPEG.
// FPS/видимость проверяются до декодирования; сокет продолжает принимать пакеты.
// nil означает потерю источника и очищает видимое изображение.
func decodeVideo(ctx context.Context, input *Latest[[]byte], view *Viewer, status *Status) {
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
	var next time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case jpg := <-input.ch:
			if len(jpg) == 0 {
				view.Clear()
				continue
			}
			if !view.Visible() {
				continue
			}
			v := view.values()
			fps := v.Int("receive_fps")
			now := time.Now()
			if fps > 0 && now.Before(next) {
				continue
			}
			scale := v.Int("decode_scale")
			if scale != 2 && scale != 4 {
				scale = 1
			}
			frame, e := engine.Decode(jpg, scale)
			if e != nil {
				status.Set("JPEG: " + e.Error())
				continue
			}
			view.Publish(frame)
			if fps > 0 {
				next = now.Add(time.Second / time.Duration(fps))
			}
		}
	}
}
