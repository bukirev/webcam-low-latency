package main

import (
	"fmt"
	"strings"

	"github.com/bukirev/webcam-low-latency/go-version/internal/brand"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func (a *Application) senderForm(title, kind string, fields [][]any, values Values) ([]Widget, func()) {
	if title == "Экран" || title == "Звук" {
		fields = senderTabFields(title, fields)
	}
	rows := make(map[string]**walk.Composite)
	visibility := make(map[string]bool)
	var region *walk.PushButton
	// Keep the edited values alive when a control is hidden. Only Apply writes
	// to the profile; changing a selector never restarts a running stream.
	refresh := func() {
		value := func(key string) string {
			if get := a.getters[kind][key]; get != nil {
				return fmt.Sprint(get())
			}
			return values.Str(key)
		}
		capture := value("capture_kind")
		video := value("video_enabled") == "true"
		audio := value("audio_enabled") == "true"
		for key, pointer := range rows {
			row := *pointer
			var shown bool
			switch title {
			case "Камера":
				shown = key != "quality" || value("codec") != "MJPG"
				if key == "adaptive_resolution" {
					shown = value("codec") != "MJPG" && value("adaptive_video") == "true"
				}
			case "Экран":
				shown = senderScreenFieldVisible(key, capture, video)
				if key == "adaptive_resolution" {
					shown = shown && value("adaptive_video") == "true"
				}
			case "Звук":
				shown = senderAudioFieldVisible(key, audio, value("system_enabled") == "true", value("mic_enabled") == "true")
			}
			if previous, known := visibility[key]; !known || previous != shown {
				row.SetVisible(shown)
				visibility[key] = shown
			}
		}
		if region != nil {
			region.SetVisible(video && capture == "Область")
		}
	}
	changed := func() {
		if !a.building {
			refresh()
		}
	}

	var basics, extra, preview []Widget
	for _, field := range fields {
		group, key, label := field[0].(string), field[1].(string), field[2].(string)
		if strings.Contains(group, "Аппаратные") || contains([]string{"preview", "buffer_ms", "output_device", "volume", "receive_mute"}, key) {
			continue
		}
		switch key {
		case "camera":
			label = "Камера"
		case "receive_fps":
			label = "Лимит FPS предпросмотра"
		case "decode_scale":
			label = "Масштаб декодирования (1/N)"
		}
		widget, get := a.fieldWidget(kind, key, label, field[3], values, changed)
		a.getters[kind][key] = get
		row := new(*walk.Composite)
		rows[key] = row
		rowWidget := Composite{AssignTo: row, Layout: VBox{MarginsZero: true}, Children: []Widget{widget}}
		switch {
		case group == "Приём и нагрузка CPU" || strings.HasPrefix(group, "Вид ") || strings.HasPrefix(group, "Обработка вида"):
			preview = append(preview, rowWidget)
		case group == "Формат и буфер" || contains([]string{"adaptive_video", "adaptive_resolution", "video_port", "audio_port", "cursor", "pause_video", "left", "top", "width", "height"}, key):
			extra = append(extra, rowWidget)
		default:
			basics = append(basics, rowWidget)
		}
		if title == "Экран" && key == "monitor" {
			basics = append(basics, Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &region, Text: "Выбрать область мышью…", OnClicked: a.selectRegion}, HSpacer{},
			}})
		}
	}
	header := Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
		PushButton{Text: "Обновить устройства", OnClicked: a.reloadDevices},
		HSpacer{},
	}}
	basics = append([]Widget{header}, basics...)
	if len(preview) > 0 {
		extra = append(extra, brand.Card("Только предпросмотр на этом ПК", preview))
	}
	var advanced *walk.Composite
	var toggle *walk.PushButton
	toggleText := func() string {
		if a.advanced[title] {
			return "Скрыть дополнительные"
		}
		return "Дополнительные настройки"
	}
	basics = append(basics, Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
		PushButton{AssignTo: &toggle, Text: toggleText(), OnClicked: func() {
			a.advanced[title] = !a.advanced[title]
			advanced.SetVisible(a.advanced[title])
			toggle.SetText(toggleText())
		}}, HSpacer{},
	}}, Composite{AssignTo: &advanced, Visible: a.advanced[title], Layout: VBox{MarginsZero: true, Spacing: 4}, Children: extra}, VSpacer{})

	return basics, refresh
}

func senderTabFields(title string, fields [][]any) [][]any {
	if title == "Звук" {
		result := append([][]any(nil), fields...)
		for _, field := range schema.Screen {
			if field[1] == "audio_port" {
				return append(result, field)
			}
		}
		return result
	}
	result := make([][]any, 1, len(fields))
	for _, field := range fields {
		switch field[1] {
		case "video_enabled":
			result[0] = field
		case "audio_port":
		default:
			result = append(result, field)
		}
	}
	return result
}

func senderScreenFieldVisible(key, capture string, video bool) bool {
	if key == "video_enabled" {
		return true
	}
	if !video {
		return false
	}
	switch key {
	case "monitor":
		return capture == "Монитор" || capture == "Область"
	case "window":
		return capture == "Окно (видимая область)"
	case "left", "top", "width", "height":
		return capture == "Область"
	default:
		return true
	}
}

func senderAudioFieldVisible(key string, audio, system, mic bool) bool {
	if key == "audio_enabled" {
		return true
	}
	if !audio {
		return false
	}
	switch key {
	case "system_device", "system_gain":
		return system
	case "mic_device", "mic_gain":
		return mic
	default:
		return true
	}
}
