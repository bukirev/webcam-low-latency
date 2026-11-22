// Package brand owns the VB2Link artwork and Walk window styling.
package brand

import (
	"bytes"
	_ "embed"
	"image/png"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const Name = "VB2Link"

var (
	background = walk.RGB(6, 18, 37)
	panel      = walk.RGB(13, 32, 57)
	input      = walk.RGB(9, 26, 48)
	textColor  = walk.RGB(230, 238, 250)
	muted      = walk.RGB(161, 188, 216)
	accent     = walk.RGB(69, 199, 255)
	focus      = walk.RGB(255, 188, 56)
)

//go:embed assets/mark.png
var markPNG []byte

//go:embed assets/splash.png
var splashPNG []byte

// All widgets and GDI resources are owned by the UI thread. The brushes and
// mark are shared for the process lifetime, including forms rebuilt by Apply.
var (
	baseBrush, panelBrush, inputBrush *walk.SolidColorBrush
	panelHandle                       win.HBRUSH
	mark                              *walk.Bitmap
	icon                              *walk.Icon
	comctl                            = windows.NewLazySystemDLL("comctl32.dll")
	findWindow                        = windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowExW")
	dwm                               = windows.NewLazySystemDLL("dwmapi.dll")
	addSubclass                       = comctl.NewProc("SetWindowSubclass")
	nextSubclass                      = comctl.NewProc("DefSubclassProc")
	dwmSetAttr                        = dwm.NewProc("DwmSetWindowAttribute")
	subclassCallback                  = syscall.NewCallback(themeMessage)
	buttons                           = make(map[win.HWND]*walk.PushButton)
	checks                            = make(map[win.HWND]bool)
	tabs                              = make(map[win.HWND]*walk.TabWidget)
)

func initBrushes() {
	if baseBrush != nil {
		return
	}
	baseBrush, _ = walk.NewSolidColorBrush(background)
	panelBrush, _ = walk.NewSolidColorBrush(panel)
	inputBrush, _ = walk.NewSolidColorBrush(input)
	panelHandle = win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(panel)})
}

func loadMark() {
	if mark != nil {
		return
	}
	if img, err := png.Decode(bytes.NewReader(markPNG)); err == nil {
		mark, _ = walk.NewBitmapFromImage(img)
		icon, _ = walk.NewIconFromImage(img)
	}
}

func Header(label string) d.Widget {
	loadMark()
	var items []d.Widget
	if mark != nil {
		items = append(items, d.ImageView{Image: mark, Mode: d.ImageViewModeZoom, MinSize: d.Size{Width: 32, Height: 32}, MaxSize: d.Size{Width: 32, Height: 32}})
	}
	items = append(items, d.Label{Text: label, Font: d.Font{Family: "Segoe UI", PointSize: 17, Bold: true}, TextColor: accent}, d.HSpacer{})
	return d.Composite{MinSize: d.Size{Height: 38}, MaxSize: d.Size{Width: 4096, Height: 38}, Layout: d.HBox{MarginsZero: true}, Children: items}
}

func Card(title string, children []d.Widget) d.Widget {
	content := []d.Widget{d.Label{Text: title, Font: d.Font{Family: "Segoe UI", PointSize: 10, Bold: true}, TextColor: accent}}
	return d.Composite{Name: "card", Layout: d.VBox{Margins: d.Margins{8, 6, 8, 6}, Spacing: 5}, Children: append(content, children...)}
}
func Window(window *walk.MainWindow) {
	initBrushes()
	loadMark()
	if icon != nil {
		_ = window.SetIcon(icon)
	}
	if dwmSetAttr.Find() == nil {
		// Windows versions without this attribute keep their normal caption.
		enabled := uint32(1)
		dwmSetAttr.Call(uintptr(window.Handle()), 20, uintptr(unsafe.Pointer(&enabled)), unsafe.Sizeof(enabled))
	}
	Apply(window)
}

func Apply(window walk.Window) {
	initBrushes()
	switch w := window.(type) {
	case *walk.MainWindow:
		w.SetBackground(baseBrush)
	case *walk.TabPage, *walk.GroupBox:
		w.SetBackground(panelBrush)
	case *walk.Composite:
		if w.Name() == "card" {
			w.SetBackground(panelBrush)
		}
	case *walk.LineEdit:
		w.SetBackground(inputBrush)
		w.SetTextColor(textColor)
	case *walk.TextEdit:
		w.SetBackground(inputBrush)
		w.SetTextColor(textColor)
	case *walk.ComboBox:
		win.SetWindowTheme(w.Handle(), windows.StringToUTF16Ptr("DarkMode_CFD"), nil)
	case *walk.Label:
		if w.TextColor() == 0 {
			w.SetTextColor(textColor)
		}
	case *walk.PushButton:
		h := w.Handle()
		if _, ok := buttons[h]; !ok && subclassParent(h) {
			minimum := w.MinSize()
			minimum.Width = max(minimum.Width, utf8.RuneCountInString(w.Text())*8+24)
			minimum.Height = max(minimum.Height, 29)
			_ = w.SetMinMaxSize(minimum, w.MaxSize())
			buttons[h] = w
			w.Disposing().Attach(func() { delete(buttons, h) })
			style := win.GetWindowLong(h, win.GWL_STYLE)
			win.SetWindowLong(h, win.GWL_STYLE, (style&^0xf)|win.BS_OWNERDRAW)
			win.InvalidateRect(h, nil, true)
		}
	case *walk.CheckBox:
		h := w.Handle()
		if !checks[h] && subclassParent(h) {
			checks[h] = true
			w.Disposing().Attach(func() { delete(checks, h) })
			empty := windows.StringToUTF16Ptr("")
			win.SetWindowTheme(h, empty, empty)
		}
	case *walk.TabWidget:
		class := windows.StringToUTF16Ptr("SysTabControl32")
		result, _, _ := findWindow.Call(uintptr(w.Handle()), 0, uintptr(unsafe.Pointer(class)), 0)
		h := win.HWND(result)
		if h != 0 && tabs[h] == nil && subclassParent(h) {
			tabs[h] = w
			w.Disposing().Attach(func() { delete(tabs, h) })
			style := win.GetWindowLong(h, win.GWL_STYLE)
			win.SetWindowLong(h, win.GWL_STYLE, style|win.TCS_OWNERDRAWFIXED|win.TCS_FIXEDWIDTH)
			dpi := max(96, int(w.DPI()))
			size := uintptr(106*dpi/96) | uintptr(28*dpi/96)<<16
			win.SendMessage(h, win.TCM_SETITEMSIZE, 0, size)
			win.InvalidateRect(h, nil, true)
		}
		for i := 0; i < w.Pages().Len(); i++ {
			Apply(w.Pages().At(i))
		}
	}
	if container, ok := window.(walk.Container); ok {
		children := container.Children()
		for i := 0; i < children.Len(); i++ {
			Apply(children.At(i))
		}
	}
}

func subclassParent(h win.HWND) bool {
	if addSubclass.Find() != nil || nextSubclass.Find() != nil {
		return false
	}
	ok, _, _ := addSubclass.Call(uintptr(win.GetParent(h)), subclassCallback, 1, 0)
	return ok != 0
}

func themeMessage(hwnd, msg, wp, lp, _, _ uintptr) uintptr {
	switch msg {
	case win.WM_DRAWITEM:
		if lp != 0 {
			item := (*win.DRAWITEMSTRUCT)(unsafe.Pointer(lp))
			if button := buttons[item.HwndItem]; button != nil {
				drawButton(item, button)
				return 1
			}
			if tab := tabs[item.HwndItem]; tab != nil && int(item.ItemID) < tab.Pages().Len() {
				i := int(item.ItemID)
				face, edge := panel, walk.RGB(47, 80, 118)
				if tab.CurrentIndex() == i {
					face, edge = walk.RGB(0, 79, 152), accent
				}
				drawFace(item, tab.Pages().At(i).Title(), face, edge, textColor)
				return 1
			}
		}
	case win.WM_CTLCOLORSTATIC, win.WM_CTLCOLORBTN:
		if checks[win.HWND(lp)] {
			win.SetTextColor(win.HDC(wp), win.COLORREF(textColor))
			win.SetBkMode(win.HDC(wp), win.TRANSPARENT)
			return uintptr(panelHandle)
		}
	}
	result, _, _ := nextSubclass.Call(hwnd, msg, wp, lp)
	return result
}

func drawButton(item *win.DRAWITEMSTRUCT, button *walk.PushButton) {
	face, edge, ink := walk.RGB(18, 47, 80), walk.RGB(51, 107, 154), textColor
	if button.Name() == "primary" {
		face, edge = walk.RGB(0, 86, 174), accent
	}
	if item.ItemState&win.ODS_SELECTED != 0 {
		face = walk.RGB(24, 74, 119)
	}
	if item.ItemState&win.ODS_DISABLED != 0 {
		face, edge, ink = input, panel, muted
	}
	if item.ItemState&win.ODS_FOCUS != 0 {
		edge = focus
	}
	drawFace(item, button.Text(), face, edge, ink)
}

func drawFace(item *win.DRAWITEMSTRUCT, label string, face, edge, ink walk.Color) {
	dc, r := item.HDC, item.RcItem
	saved := win.SaveDC(dc)
	outer := win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(edge)})
	inner := win.CreateBrushIndirect(&win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.COLORREF(face)})
	win.SelectObject(dc, win.GetStockObject(win.NULL_PEN))
	win.SelectObject(dc, win.HGDIOBJ(outer))
	win.RoundRect(dc, r.Left, r.Top, r.Right, r.Bottom, 7, 7)
	win.SelectObject(dc, win.HGDIOBJ(inner))
	win.RoundRect(dc, r.Left+1, r.Top+1, r.Right-1, r.Bottom-1, 6, 6)
	if font := win.SendMessage(item.HwndItem, win.WM_GETFONT, 0, 0); font != 0 {
		win.SelectObject(dc, win.HGDIOBJ(font))
	}
	win.SetTextColor(dc, win.COLORREF(ink))
	win.SetBkMode(dc, win.TRANSPARENT)
	caption, _ := windows.UTF16FromString(label)
	if len(caption) > 1 {
		r.Left += 4
		r.Right -= 4
		win.DrawTextEx(dc, &caption[0], int32(len(caption)-1), &r,
			win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE|win.DT_END_ELLIPSIS, nil)
	}
	win.RestoreDC(dc, saved)
	win.DeleteObject(win.HGDIOBJ(outer))
	win.DeleteObject(win.HGDIOBJ(inner))
}

type Splash struct {
	window *walk.MainWindow
	image  *walk.Bitmap
	start  time.Time
}

func ShowSplash() *Splash {
	initBrushes()
	source, err := png.Decode(bytes.NewReader(splashPNG))
	if err != nil {
		return nil
	}
	bmp, err := walk.NewBitmapFromImage(source)
	if err != nil {
		return nil
	}
	s := &Splash{image: bmp}
	if err := (d.MainWindow{AssignTo: &s.window, Title: Name + " — запуск", Visible: false,
		Size: d.Size{Width: 680, Height: 365}, Layout: d.VBox{MarginsZero: true, SpacingZero: true},
		Children: []d.Widget{
			d.ImageView{Image: bmp, Mode: d.ImageViewModeZoom, StretchFactor: 1},
			d.Composite{Layout: d.HBox{Margins: d.Margins{18, 8, 18, 10}}, Children: []d.Widget{
				d.Label{Text: "Запуск VB2Link…", TextColor: muted}, d.HSpacer{},
			}},
		},
	}).Create(); err != nil {
		bmp.Dispose()
		return nil
	}
	s.window.SetBackground(baseBrush)
	h := s.window.Handle()
	style := win.GetWindowLong(h, win.GWL_STYLE)
	win.SetWindowLong(h, win.GWL_STYLE, style&^(win.WS_CAPTION|win.WS_THICKFRAME))
	ex := win.GetWindowLong(h, win.GWL_EXSTYLE)
	win.SetWindowLong(h, win.GWL_EXSTYLE, (ex&^win.WS_EX_APPWINDOW)|win.WS_EX_TOOLWINDOW)
	screen := win.MONITORINFO{CbSize: uint32(unsafe.Sizeof(win.MONITORINFO{}))}
	bounds := s.window.BoundsPixels()
	if win.GetMonitorInfo(win.MonitorFromWindow(h, win.MONITOR_DEFAULTTOPRIMARY), &screen) {
		r := screen.RcWork
		win.SetWindowPos(h, win.HWND_TOP, r.Left+(r.Right-r.Left-int32(bounds.Width))/2,
			r.Top+(r.Bottom-r.Top-int32(bounds.Height))/2, int32(bounds.Width), int32(bounds.Height), win.SWP_FRAMECHANGED|win.SWP_NOACTIVATE)
	}
	s.window.Show()
	win.RedrawWindow(h, nil, 0, win.RDW_INVALIDATE|win.RDW_UPDATENOW|win.RDW_ALLCHILDREN)
	s.start = time.Now()
	return s
}

func (s *Splash) Close() {
	if s == nil || s.window == nil {
		return
	}
	s.window.Dispose()
	s.window = nil
	s.image.Dispose()
}

// Reveal after initialization, keeping quick starts visible for only a short
// moment. No blocking sleep and no auxiliary UI thread are required.
func (s *Splash) Reveal(window *walk.MainWindow) {
	reveal := func() { s.Close(); window.Show(); window.Activate() }
	if s == nil {
		reveal()
		return
	}
	delay := 600*time.Millisecond - time.Since(s.start)
	if delay <= 0 {
		reveal()
		return
	}
	time.AfterFunc(delay, func() { window.Synchronize(reveal) })
}
