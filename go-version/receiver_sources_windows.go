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
	cameraOptions, readCameras := receiverCameraOptions(draft.selectedReceiverCameras(), found["camera"])
	for _, feed := range []struct{ kind, title string }{{"screen", "Экран"}, {"audio", "Звук"}} {
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
			cameraOptions,
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
		cameras := readCameras()
		draft.ReceiverCameras = nil
		if len(cameras) != 0 {
			draft.ReceiverSources["camera"] = cameras[0]
			draft.ReceiverCameras = cameras[1:]
		}
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

func receiverCameraOptions(saved []receiverSource, found []lanSender) (Widget, func() []receiverSource) {
	discovered := make(map[netip.AddrPort]receiverSource, len(found))
	for _, sender := range found {
		endpoint := netip.AddrPortFrom(sender.ip, sender.port)
		discovered[endpoint] = receiverSource{Endpoint: endpoint, Name: sender.name}
	}
	type choice struct {
		source receiverSource
		box    *walk.CheckBox
	}
	options := make([]*choice, 0, len(saved)+len(found))
	var children []Widget
	add := func(source receiverSource, checked, available bool) {
		item := &choice{source: source}
		options = append(options, item)
		label := receiverSourceLabel(source)
		if !available {
			label += " · не найдена"
		}
		children = append(children, Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			CheckBox{AssignTo: &item.box, Text: label, Checked: checked, ToolTipText: label}, HSpacer{},
		}})
	}
	for _, source := range saved {
		if current, ok := discovered[source.Endpoint]; ok {
			add(current, true, true)
		} else {
			add(source, true, false)
		}
		delete(discovered, source.Endpoint)
	}
	for _, sender := range found {
		endpoint := netip.AddrPortFrom(sender.ip, sender.port)
		if source, ok := discovered[endpoint]; ok {
			add(source, false, true)
			delete(discovered, endpoint)
		}
	}
	var optionsWidget Widget = Label{Text: "Сетевые камеры не найдены."}
	if len(children) != 0 {
		height := min(len(children)*26+12, 150)
		optionsWidget = ScrollView{
			MinSize: Size{Height: height}, MaxSize: Size{Height: height},
			Layout: VBox{Margins: Margins{6, 4, 6, 4}, Spacing: 3}, Children: children,
		}
	}
	return Composite{Layout: VBox{MarginsZero: true, Spacing: 4}, Children: []Widget{
		Label{Text: "Камеры · отметьте нужные"},
		optionsWidget,
		Label{Text: "Если камер не выбрано, используются общий IP и порт камеры."},
	}}, func() []receiverSource {
		var selected []receiverSource
		for _, item := range options {
			if item.box.Checked() {
				selected = append(selected, item.source)
			}
		}
		return selected
	}
}

func (a *Application) receiverCameraCards(cfg Config) []Widget {
	var cards []Widget
	for _, source := range cfg.selectedReceiverCameras() {
		key := source.cameraKey()
		a.streams[key] = &streamControls{}
		cards = append(cards, brand.Card(receiverSourceLabel(source), []Widget{a.streamRow(key, "")}))
	}
	return cards
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
