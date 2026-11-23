package main

import (
	"fmt"
	"slices"
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
	var region *walk.Composite
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

	var left, right, extra, preview []Widget
	for _, field := range fields {
		group, key, label := field[0].(string), field[1].(string), field[2].(string)
		if strings.Contains(group, "Аппаратные") || contains([]string{"preview", "buffer_ms", "output_device", "volume", "receive_mute", "camera_port"}, key) {
			continue
		}
		switch key {
		case "camera":
			label = "Камера"
		case "receive_fps":
			label = "Лимит FPS предпросмотра"
		case "decode_scale":
			label = "Масштаб декодирования (1/N)"
		case "max_width":
			label = "Макс. ширина экрана, пикс."
		}
		widget, get := a.fieldWidget(kind, key, label, field[3], values, true, changed)
		a.getters[kind][key] = get
		row := new(*walk.Composite)
		rows[key] = row
		rowWidget := Composite{AssignTo: row, Layout: VBox{MarginsZero: true}, Children: []Widget{widget}}
		switch {
		case group == "Приём и нагрузка CPU" || strings.HasPrefix(group, "Вид ") || strings.HasPrefix(group, "Обработка вида"):
			preview = append(preview, rowWidget)
		case group == "Формат и буфер" || contains([]string{"adaptive_video", "adaptive_resolution", "video_port", "audio_port", "cursor", "pause_video", "left", "top", "width", "height"}, key):
			extra = append(extra, rowWidget)
		case contains([]string{"fps", "max_width", "quality", "codec", "mic_enabled", "mic_device", "mic_gain", "send_mute"}, key):
			right = append(right, rowWidget)
		default:
			left = append(left, rowWidget)
		}
		if title == "Экран" && key == "window" {
			left = append(left, Composite{AssignTo: &region, Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{Text: "Выбрать область мышью…", OnClicked: a.selectRegion}, HSpacer{},
			}})
		}
	}
	additional := []Widget{pairedSettings(extra)}
	if len(preview) > 0 {
		additional = append(additional, brand.Card("Только предпросмотр на этом ПК", []Widget{pairedSettings(preview)}))
	}
	var advanced *walk.Composite
	var toggle *walk.PushButton
	toggleText := func() string {
		if a.preferences.Advanced[title] {
			return "Скрыть дополнительные"
		}
		return "Дополнительные настройки"
	}
	return []Widget{
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			PushButton{Text: "Обновить устройства", OnClicked: a.reloadDevices}, HSpacer{},
			PushButton{AssignTo: &toggle, Text: toggleText(), OnClicked: func() {
				a.preferences.Advanced[title] = !a.preferences.Advanced[title]
				advanced.SetVisible(a.preferences.Advanced[title])
				toggle.SetText(toggleText())
			}},
		}},
		settingsColumns(left, right),
		Composite{AssignTo: &advanced, Visible: a.preferences.Advanced[title], Layout: VBox{MarginsZero: true, Spacing: 6}, Children: additional},
		VSpacer{},
	}, refresh
}

func settingsColumns(left, right []Widget) Widget {
	return Composite{Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 14}, Children: []Widget{
		Composite{Column: 0, Layout: VBox{MarginsZero: true, Spacing: 5}, Children: append(left, HSpacer{}, VSpacer{})},
		Composite{Column: 1, Layout: VBox{MarginsZero: true, Spacing: 5}, Children: append(right, HSpacer{}, VSpacer{})},
	}}
}

func pairedSettings(fields []Widget) Widget {
	mid := (len(fields) + 1) / 2
	return settingsColumns(fields[:mid:mid], fields[mid:])
}

func screenWidthField(value string) (Widget, func() any) {
	const original = "Исходная"
	presets := []string{original, "640", "800", "960", "1024", "1280", "1366", "1600", "1920", "2560", "3840", "7680"}
	if value == "0" {
		value = original
	}
	index := slices.Index(presets, value)
	if index < 0 {
		index = len(presets)
		presets = append(presets, value)
	}
	var box *walk.ComboBox
	return ComboBox{AssignTo: &box, Model: presets, CurrentIndex: index, Editable: true,
			ToolTipText: "Выберите ширину или введите число. Исходная сохраняет ширину захвата."},
		func() any {
			width := strings.TrimSpace(box.Text())
			if width == original {
				return "0"
			}
			return width
		}
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
