package main

import (
	"fmt"
	"log"
	"net/netip"
	"slices"
	"unicode/utf8"

	"github.com/bukirev/webcam-low-latency/go-version/internal/brand"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func receiverSourceLabel(source receiverSource) string {
	if source.Name == "" {
		return source.Endpoint.String()
	}
	return fmt.Sprintf("%s (%s)", source.Name, source.Endpoint)
}

func (a *Application) findReceiverSources() {
	draft := a.draft()
	a.message.SetText("Поиск активных источников в локальной сети…")
	found := scanReceiverSources(draft)
	var dialog *walk.Dialog
	var cancel, accept *walk.PushButton
	var fields []Widget
	var readSelections []func() (string, receiverSource)
	for _, feed := range []struct{ kind, title string }{{"camera", "Камера"}, {"screen", "Экран"}, {"audio", "Звук"}} {
		model := []string{"Общий IP и порт из настроек"}
		options := []receiverSource{{}}
		saved := draft.ReceiverSources[feed.kind]
		index := 0
		for _, sender := range found[feed.kind] {
			source := receiverSource{Endpoint: netip.AddrPortFrom(sender.ip, sender.port), Name: sender.name}
			if source.Endpoint == saved.Endpoint {
				index = len(options)
			}
			options = append(options, source)
			model = append(model, receiverSourceLabel(source))
		}
		if index == 0 && saved.Endpoint.IsValid() {
			index = len(options)
			options = append(options, saved)
			model = append(model, receiverSourceLabel(saved)+" · не найден")
		}
		var selection *walk.ComboBox
		fields = append(fields, Label{Text: feed.title}, ComboBox{AssignTo: &selection, Model: model, CurrentIndex: index})
		readSelections = append(readSelections, func() (string, receiverSource) {
			return feed.kind, options[max(0, selection.CurrentIndex())]
		})
	}
	total := len(found["camera"]) + len(found["screen"]) + len(found["audio"])
	summary := fmt.Sprintf("Источников найдено: %d", total)
	if total == 0 {
		summary = "Источники не найдены. Проверьте, запущена ли передача."
	}
	err := (Dialog{
		AssignTo: &dialog, Title: "Источники в сети", FixedSize: true, Visible: false,
		MinSize: Size{Width: 490}, DefaultButton: &accept, CancelButton: &cancel,
		Font: Font{Family: "Segoe UI", PointSize: 10}, Layout: VBox{},
		Children: []Widget{
			Label{Text: summary},
			Composite{Layout: Grid{Columns: 2, MarginsZero: true}, Children: fields},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{},
				PushButton{AssignTo: &accept, Name: "primary", Text: "Выбрать", OnClicked: func() { dialog.Accept() }},
				PushButton{AssignTo: &cancel, Text: "Отмена", OnClicked: func() { dialog.Cancel() }},
			}},
		},
	}).Create(a.window)
	if err != nil {
		a.error(err)
		return
	}
	brand.Apply(dialog)
	// Read the selections before the modal window disposes its controls.
	dialog.Closing().Attach(func(_ *bool, _ walk.CloseReason) {
		if dialog.Result() != walk.DlgCmdOK {
			return
		}
		draft.ReceiverSources = make(map[string]receiverSource)
		for _, get := range readSelections {
			kind, source := get()
			if source.Endpoint.IsValid() {
				draft.ReceiverSources[kind] = source
			}
		}
		if len(draft.ReceiverSources) == 0 {
			draft.ReceiverSources = nil
		}
	})
	if dialog.Run() != walk.DlgCmdOK {
		a.message.SetText(summary)
		return
	}
	a.error(a.forms(draft))
	a.message.SetText("Источники выбраны. Нажмите «Применить» или запустите приём.")
}

func (a *Application) configureCatalog(c Config) {
	if c.Role != "server" || c.Screen.Int("video_port") == lanCatalogPort || c.Screen.Int("audio_port") == lanCatalogPort {
		a.closeCatalog()
		return
	}
	if a.catalog != nil {
		return
	}
	service, err := openLANCatalog(lanCatalogPort)
	if err != nil {
		log.Printf("LAN SOURCE CATALOG: %v", err)
		return
	}
	a.catalog = service
}

func (a *Application) closeCatalog() {
	if a.catalog != nil {
		a.catalog.conn.Close()
		a.catalog = nil
	}
}

func (a *Application) publishCatalog(c Config) {
	if a.catalog == nil {
		return
	}
	var feeds []lanFeed
	host := a.catalog.hostname
	add := func(kind, name string, port int) {
		label := discoveryName(host + " — " + name)
		if len(label) > 44 {
			label = label[:44]
			for !utf8.ValidString(label) {
				label = label[:len(label)-1]
			}
		}
		feeds = append(feeds, lanFeed{kind, uint16(port), label})
	}
	for _, key := range c.streamKeys() {
		if session := a.sessions[key]; session == nil || !session.active.Load() {
			continue
		}
		switch key {
		case "camera":
			add("camera", c.primaryCameraName(), 5001)
		case "screen":
			if c.Screen.Bool("video_enabled") {
				add("screen", "Экран", c.Screen.Int("video_port"))
			}
			if c.Screen.Bool("audio_enabled") {
				add("audio", "Звук", c.Screen.Int("audio_port"))
			}
		default:
			if camera, ok := c.extraCamera(key); ok {
				add("camera", camera.Name, camera.Port)
			}
		}
	}
	if previous := a.catalog.feeds.Load(); previous == nil || !slices.Equal(*previous, feeds) {
		a.catalog.feeds.Store(&feeds)
	}
}
