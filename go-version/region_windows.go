package main

import (
	"fmt"
	"image"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

func screenBitmap(r CaptureRect) (*walk.Bitmap, error) {
	desktop := win.GetDC(0)
	defer win.ReleaseDC(0, desktop)
	dc := win.CreateCompatibleDC(desktop)
	defer win.DeleteDC(dc)
	info := win.BITMAPINFOHEADER{BiSize: 40, BiWidth: int32(r.W), BiHeight: -int32(r.H), BiPlanes: 1, BiBitCount: 32}
	var bits unsafe.Pointer
	bmp := win.CreateDIBSection(desktop, &info, win.DIB_RGB_COLORS, &bits, 0, 0)
	if bmp == 0 {
		return nil, fmt.Errorf("не удалось создать снимок монитора")
	}
	defer win.DeleteObject(win.HGDIOBJ(bmp))
	old := win.SelectObject(dc, win.HGDIOBJ(bmp))
	defer win.SelectObject(dc, old)
	if !win.BitBlt(dc, 0, 0, int32(r.W), int32(r.H), desktop, int32(r.X), int32(r.Y), win.SRCCOPY|win.CAPTUREBLT) {
		return nil, fmt.Errorf("не удалось получить снимок монитора")
	}
	img := image.NewRGBA(image.Rect(0, 0, r.W, r.H))
	data := unsafe.Slice((*byte)(bits), r.W*r.H*4)
	for i := 0; i < len(data); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = data[i+2], data[i+1], data[i], 255
	}
	return walk.NewBitmapFromImage(img)
}

func (a *Application) selectRegion() {
	draft := a.draft()
	v := draft.Screen.Clone()
	v["capture_kind"] = "Монитор"
	r, e := screenRect(v)
	if e != nil {
		a.error(e)
		return
	}
	bmp, e := screenBitmap(r)
	if e != nil {
		a.error(e)
		return
	}
	defer bmp.Dispose()
	pen, e := walk.NewCosmeticPen(walk.PenSolid, walk.RGB(255, 70, 40))
	if e != nil {
		a.error(e)
		return
	}
	defer pen.Dispose()
	var dlg *walk.Dialog
	var canvas *walk.CustomWidget
	var hint *walk.Label
	var accept, cancel *walk.PushButton
	var shown walk.Rectangle
	var start, end walk.Point
	dragging, selected := false, false
	clamp := func(x, y int) walk.Point {
		return walk.Point{X: max(shown.X, min(x, shown.X+shown.Width)), Y: max(shown.Y, min(y, shown.Y+shown.Height))}
	}
	selection := func() walk.Rectangle {
		return walk.Rectangle{X: min(start.X, end.X), Y: min(start.Y, end.Y), Width: absInt(start.X - end.X), Height: absInt(start.Y - end.Y)}
	}
	e = (Dialog{AssignTo: &dlg, Title: "Выберите область на снимке монитора", Size: Size{1000, 700}, MinSize: Size{500, 350}, Font: Font{Family: "Segoe UI", PointSize: 10}, Layout: VBox{}, DefaultButton: &accept, CancelButton: &cancel, Children: []Widget{
		Label{AssignTo: &hint, Text: "Зажмите левую кнопку и выделите прямоугольник. Затем нажмите «Выбрать»."},
		CustomWidget{AssignTo: &canvas, PaintMode: PaintNoErase, InvalidatesOnResize: true, PaintPixels: func(c *walk.Canvas, b walk.Rectangle) error {
			scale := min(float64(b.Width)/float64(r.W), float64(b.Height)/float64(r.H))
			w, h := max(1, int(float64(r.W)*scale)), max(1, int(float64(r.H)*scale))
			shown = walk.Rectangle{X: (b.Width - w) / 2, Y: (b.Height - h) / 2, Width: w, Height: h}
			if e := c.DrawImageStretchedPixels(bmp, shown); e != nil {
				return e
			}
			if selected || dragging {
				return c.DrawRectanglePixels(pen, selection())
			}
			return nil
		}, OnMouseDown: func(x, y int, button walk.MouseButton) {
			if button != walk.LeftButton {
				return
			}
			start = clamp(x, y)
			end = start
			dragging = true
			selected = false
			invoke(syscall.NewLazyDLL("user32.dll").NewProc("SetCapture"), uintptr(canvas.Handle()))
		},
			OnMouseMove: func(x, y int, button walk.MouseButton) {
				if dragging {
					end = clamp(x, y)
					canvas.Invalidate()
				}
			},
			OnMouseUp: func(x, y int, button walk.MouseButton) {
				if !dragging {
					return
				}
				end = clamp(x, y)
				dragging = false
				selected = selection().Width > 1 && selection().Height > 1
				invoke(syscall.NewLazyDLL("user32.dll").NewProc("ReleaseCapture"))
				canvas.Invalidate()
				accept.SetEnabled(selected)
			},
		},
		Composite{Layout: HBox{}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &accept, Text: "Выбрать", Enabled: false, OnClicked: func() {
			if selected {
				dlg.Accept()
			}
		}}, PushButton{AssignTo: &cancel, Text: "Отмена", OnClicked: func() { dlg.Cancel() }}}},
	}}).Create(a.window)
	if e != nil {
		a.error(e)
		return
	}
	defer dlg.Dispose()
	if dlg.Run() != walk.DlgCmdOK || !selected {
		return
	}
	s := selection()
	x, y := r.X+(s.X-shown.X)*r.W/shown.Width, r.Y+(s.Y-shown.Y)*r.H/shown.Height
	w, h := max(1, s.Width*r.W/shown.Width), max(1, s.Height*r.H/shown.Height)
	draft.Screen["capture_kind"] = "Область"
	for k, n := range map[string]int{"left": x, "top": y, "width": w, "height": h} {
		draft.Screen[k] = strconv.Itoa(n)
	}
	a.error(a.forms(draft))
	a.message.SetText(fmt.Sprintf("Выбрана область %dx%d (%d,%d). Нажмите «Применить».", w, h, x, y))
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
