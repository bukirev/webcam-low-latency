package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/bukirev/webcam-low-latency/go-version/internal/brand"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// Old profiles store an enumeration index or an index followed by a name.
// Select the stable Media Foundation identifier when the corresponding device is present.
func cameraDeviceID(devices []NamedDevice, value string) string {
	for _, d := range devices {
		if strings.EqualFold(d.ID, value) {
			return d.ID
		}
	}
	index, label, labeled := strings.Cut(value, ":")
	i, err := strconv.Atoi(index)
	if err == nil {
		if len(devices) > i && i >= 0 && (!labeled || strings.EqualFold(devices[i].Name, strings.TrimSpace(label))) {
			return devices[i].ID
		}
		if labeled {
			label = strings.TrimSpace(label)
		} else {
			return value
		}
	} else {
		label = value
	}
	for _, d := range devices {
		if strings.EqualFold(d.Name, label) {
			return d.ID
		}
	}
	return value
}

func cameraDeviceSelection(devices []NamedDevice, savedID string) (string, int) {
	id := cameraDeviceID(devices, savedID)
	return id, slices.IndexFunc(devices, func(device NamedDevice) bool { return device.ID == id })
}

func (a *Application) addCamera() {
	next := a.draft()
	usedPorts := map[int]bool{next.Screen.Int("video_port"): true, next.Screen.Int("audio_port"): true}
	usedDevices := map[string]bool{cameraDeviceID(a.devices["camera"], next.Camera.Str("camera")): true}
	for _, camera := range next.Cameras {
		usedPorts[camera.Port] = true
		usedDevices[cameraDeviceID(a.devices["camera"], camera.Camera.Str("camera"))] = true
	}
	for port := firstExtraCameraPort; port <= lastExtraCameraPort; port++ {
		if usedPorts[port] {
			continue
		}
		camera := cameraChannel{
			Name: fmt.Sprintf("Камера %d", port-firstExtraCameraPort+2), Port: port,
			Camera: schema.Defaults["camera"].Clone(),
		}
		camera.Camera["camera"] = ""
		for _, device := range a.devices["camera"] {
			if !usedDevices[device.ID] {
				camera.Camera["camera"] = device.ID
				break
			}
		}
		next.Cameras = append(next.Cameras, camera)
		a.error(a.forms(next))
		a.message.SetText("Выберите устройство дополнительной камеры и примените настройки.")
		return
	}
	a.error(fmt.Errorf("все порты дополнительных камер заняты"))
}

func (a *Application) removeCamera(port int) {
	next := a.draft()
	next.Cameras = slices.DeleteFunc(next.Cameras, func(camera cameraChannel) bool { return camera.Port == port })
	a.error(a.forms(next))
	a.message.SetText("Нажмите «Применить», чтобы удалить поток камеры.")
}

func (a *Application) cameraCards(c Config) []Widget {
	var primary *walk.LineEdit
	a.primaryCameraName = func() string { return strings.TrimSpace(primary.Text()) }
	cards := []Widget{Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
		PushButton{Text: "Добавить камеру", OnClicked: a.addCamera},
		Label{Text: "Имя основной камеры · UDP 5001"}, LineEdit{AssignTo: &primary, Text: c.primaryCameraName()},
	}}}
	for _, camera := range c.Cameras {
		var name *walk.LineEdit
		var left, right []Widget
		getters := make(map[string]func() any)
		left = append(left, Composite{Layout: VBox{MarginsZero: true, Spacing: 2}, Children: []Widget{
			Label{Text: "Имя камеры в сети"}, LineEdit{AssignTo: &name, Text: camera.Name},
		}})
		for _, field := range schema.Camera {
			key := field[1].(string)
			if !contains([]string{"camera", "resolution", "fps", "codec", "quality", "adaptive_video", "adaptive_resolution"}, key) {
				continue
			}
			label := field[2].(string)
			if key == "camera" {
				label = "Устройство"
			}
			widget, get := a.fieldWidget("camera", key, label, field[3], camera.Camera, true, nil)
			getters[key] = get
			if key == "camera" || key == "resolution" || key == "codec" {
				left = append(left, widget)
			} else {
				right = append(right, widget)
			}
		}
		a.cameraDrafts = append(a.cameraDrafts, func() cameraChannel {
			draft := camera
			draft.Camera = camera.Camera.Clone()
			for key, get := range getters {
				draft.Camera[key] = get()
			}
			if value := strings.TrimSpace(name.Text()); value != "" {
				draft.Name = value
			}
			return draft
		})
		key := camera.key()
		a.streams[key] = &streamControls{}
		cards = append(cards, brand.Card(fmt.Sprintf("Дополнительная камера · UDP %d", camera.Port), []Widget{
			settingsColumns(left, right),
			a.streamRow(key, "Камера"),
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{Text: "Удалить камеру", OnClicked: func() { a.removeCamera(camera.Port) }}, HSpacer{},
			}},
		}))
	}
	return cards
}
