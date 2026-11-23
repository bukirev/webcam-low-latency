package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bukirev/webcam-low-latency/go-version/internal/brand"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const vb2linkReleases = "https://github.com/bukirev/webcam-low-latency/releases"

func projectHeader(title string, about func()) Widget {
	return Composite{MaxSize: Size{Height: 38}, Layout: HBox{MarginsZero: true}, Children: []Widget{
		brand.Header(title),
		PushButton{Text: "О проекте", OnClicked: about},
	}}
}

func showAbout(owner walk.Form) {
	var dialog *walk.Dialog
	var closeButton *walk.PushButton
	if err := (Dialog{
		AssignTo: &dialog, Title: "О проекте — " + brand.Name, Visible: false,
		FixedSize: true, DefaultButton: &closeButton, CancelButton: &closeButton,
		Font:   Font{Family: "Segoe UI", PointSize: 10},
		Layout: VBox{Margins: Margins{14, 12, 14, 12}, Spacing: 10},
		Children: []Widget{
			brand.Header(brand.Name),
			Label{Text: "Версия " + version},
			Label{Text: "Камера, экран и звук по локальной сети. Приём в OBS."},
			Label{Text: "Перед установкой обновления закройте VB2Link."},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{Name: "primary", Text: "Открыть обновлятор", OnClicked: func() {
					if openUpdateManager(dialog) {
						dialog.Accept()
					}
				}},
				PushButton{AssignTo: &closeButton, Text: "Закрыть", OnClicked: func() { dialog.Cancel() }},
			}},
		},
	}).Create(owner); err != nil {
		log.Printf("ABOUT: %v", err)
		walk.MsgBox(owner, brand.Name, "Не удалось открыть сведения о проекте.", walk.MsgBoxIconError)
		return
	}
	brand.Apply(dialog)
	dialog.Run()
}

func openUpdateManager(owner walk.Form) bool {
	err := launchUpdateManager()
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		const message = "Единый обновлятор VB2Link не установлен. Открыть страницу выпусков, чтобы скачать VB2Link-Setup-Windows-x64.exe?"
		if walk.MsgBox(owner, brand.Name, message, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
			return false
		}
		if err = windows.ShellExecute(windows.Handle(owner.Handle()), windows.StringToUTF16Ptr("open"),
			windows.StringToUTF16Ptr(vb2linkReleases), nil, nil, win.SW_SHOWNORMAL); err == nil {
			return false
		}
		log.Printf("OPEN VB2LINK RELEASES: %v", err)
		walk.MsgBox(owner, brand.Name, "Не удалось открыть браузер. Страница выпусков: "+vb2linkReleases, walk.MsgBoxIconError)
		return false
	}
	log.Printf("OPEN VB2LINK UPDATER: %v", err)
	walk.MsgBox(owner, brand.Name, "Не удалось запустить обновлятор. Проверьте установку VB2Link.", walk.MsgBoxIconError)
	return false
}

func launchUpdateManager() error {
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return err
	}
	// The unified installer registers this same per-user executable in Start.
	dir := filepath.Join(local, "WebcamGo")
	path := filepath.Join(dir, "VB2Link-Setup.exe")
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("updater is not a regular file: %s", path)
	}
	cmd := exec.Command(path)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
