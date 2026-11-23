package brand

import (
	"bytes"
	_ "embed"
	"image/png"
	"math/rand/v2"
	"unsafe"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

//go:embed assets/splash-gloss.png
var glossPNG []byte

//go:embed assets/splash-monitors.png
var monitorsPNG []byte

const authorWebsite = "https://vb2tr.ru/"

// Welcome runs on the same UI thread as the panel it introduces.
type Welcome struct {
	window   *walk.Dialog
	image    *walk.Bitmap
	progress *walk.ProgressBar
	status   *walk.Label
	proceed  *walk.PushButton
	ready    bool
}

func ShowWelcome(version string) *Welcome {
	initBrushes()
	artwork := [][]byte{splashPNG, glossPNG, monitorsPNG}
	img, err := png.Decode(bytes.NewReader(artwork[rand.IntN(len(artwork))]))
	if err != nil {
		return nil
	}
	bitmap, err := walk.NewBitmapFromImage(img)
	if err != nil {
		return nil
	}
	s := &Welcome{image: bitmap}
	err = (d.Dialog{
		AssignTo: &s.window, Title: Name + " — добро пожаловать", Visible: false, FixedSize: true,
		Size: d.Size{Width: 740, Height: 620}, Font: d.Font{Family: "Segoe UI", PointSize: 10},
		Layout:        d.VBox{MarginsZero: true, SpacingZero: true},
		DefaultButton: &s.proceed, CancelButton: &s.proceed,
		Children: []d.Widget{
			d.ImageView{Image: bitmap, Mode: d.ImageViewModeZoom, StretchFactor: 1},
			d.Composite{Name: "card", Layout: d.VBox{Margins: d.Margins{Left: 20, Top: 12, Right: 20, Bottom: 10}, Spacing: 8},
				Children: []d.Widget{
					d.Label{Text: "Добро пожаловать в " + Name, TextColor: textColor, Font: d.Font{Family: "Segoe UI", PointSize: 18, Bold: true}},
					d.Composite{Layout: d.HBox{MarginsZero: true}, Children: []d.Widget{
						d.PushButton{AssignTo: &s.proceed, Name: "primary", Text: "Спасибо, продолжить", Enabled: false,
							MinSize: d.Size{Height: 38}, StretchFactor: 1,
							ToolTipText: "Перейти к панели · Enter или Esc", OnClicked: s.continueToPanel},
						d.PushButton{Text: "Отблагодарить автора", ToolTipText: "Открыть " + authorWebsite + " в браузере",
							MinSize: d.Size{Height: 38}, StretchFactor: 1, OnClicked: s.openAuthorWebsite},
					}},
					d.Composite{Layout: d.HBox{MarginsZero: true}, Children: []d.Widget{
						d.Label{AssignTo: &s.status, Text: "Подготовка панели…", TextColor: muted, MinSize: d.Size{Width: 240}},
						d.HSpacer{}, d.Label{Text: version, TextColor: muted},
					}},
				}},
			d.ProgressBar{AssignTo: &s.progress, MarqueeMode: true, MaxSize: d.Size{Height: 6}, AlwaysConsumeSpace: true},
		},
	}).Create(nil)
	if err != nil {
		s.Close()
		return nil
	}
	s.window.SetBackground(baseBrush)
	Apply(s.window)
	s.window.Closing().Attach(func(cancel *bool, _ walk.CloseReason) { *cancel = !s.ready })

	h := s.window.Handle()
	style := uint32(win.GetWindowLong(h, win.GWL_STYLE))
	win.SetWindowLong(h, win.GWL_STYLE, int32((style&^(win.WS_CAPTION|win.WS_THICKFRAME))|win.WS_POPUP))
	extended := win.GetWindowLong(h, win.GWL_EXSTYLE)
	win.SetWindowLong(h, win.GWL_EXSTYLE, (extended&^win.WS_EX_APPWINDOW)|win.WS_EX_TOOLWINDOW)
	screen := win.MONITORINFO{CbSize: uint32(unsafe.Sizeof(win.MONITORINFO{}))}
	if win.GetMonitorInfo(win.MonitorFromWindow(h, win.MONITOR_DEFAULTTOPRIMARY), &screen) {
		work := screen.RcWork
		size := s.window.SizePixels()
		width, height := min(int32(size.Width), work.Right-work.Left), min(int32(size.Height), work.Bottom-work.Top)
		win.SetWindowPos(h, win.HWND_TOP, work.Left+(work.Right-work.Left-width)/2,
			work.Top+(work.Bottom-work.Top-height)/2, width, height, win.SWP_FRAMECHANGED|win.SWP_NOACTIVATE)
	}
	// Dialog.Show fits to the content. Keep the work-area bounds set above.
	s.window.FormBase.Show()
	paintStartupWindow(h)
	return s
}

func paintStartupWindow(hwnd win.HWND) {
	win.RedrawWindow(hwnd, nil, 0, win.RDW_INVALIDATE|win.RDW_UPDATENOW|win.RDW_ALLCHILDREN)
}

func (s *Welcome) continueToPanel() {
	if s.ready {
		s.window.Accept()
	}
}

func (s *Welcome) openAuthorWebsite() {
	verb := windows.StringToUTF16Ptr("open")
	url := windows.StringToUTF16Ptr(authorWebsite)
	if err := windows.ShellExecute(windows.Handle(s.window.Handle()), verb, url, nil, nil, win.SW_SHOWNORMAL); err != nil {
		s.status.SetText("Сайт автора: " + authorWebsite)
	}
}

func (s *Welcome) Close() {
	if s == nil {
		return
	}
	if s.window != nil {
		s.window.Dispose()
		s.window = nil
	}
	if s.image != nil {
		s.image.Dispose()
		s.image = nil
	}
}

// Reveal paints the initialized panel under its modal welcome window and waits
// for confirmation. The panel and the dialog share one Windows UI thread.
func (s *Welcome) Reveal(panel *walk.MainWindow) {
	if s != nil {
		win.SetWindowLongPtr(s.window.Handle(), win.GWLP_HWNDPARENT, uintptr(panel.Handle()))
		panel.SetEnabled(false)
		panel.Show()
		paintStartupWindow(panel.Handle())
		s.ready = true
		s.status.SetText("Панель готова")
		_ = s.progress.SetMarqueeMode(false)
		s.progress.SetVisible(false)
		s.proceed.SetEnabled(true)
		_ = s.window.Activate()
		_ = s.proceed.SetFocus()
		s.window.FormBase.Run()
		s.Close()
		panel.SetEnabled(true)
	} else {
		panel.Show()
	}
	_ = panel.Activate()
}
