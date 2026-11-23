package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
)

var version = "5.0 OBS preview 6"

//go:embed settings_schema.json
var schemaFS embed.FS

// Values сохраняет совместимость JSON с Python: числовые параметры могут быть строками.
// Преобразователи ниже не заменяют validate; неизвестный ключ Str даст "<nil>".
type Values map[string]any

func (v Values) Str(k string) string {
	if s, ok := v[k].(string); ok {
		return s
	}
	return fmt.Sprint(v[k])
}
func (v Values) Num(k string) float64 {
	switch n := v[k].(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}
func (v Values) Int(k string) int   { return int(v.Num(k)) }
func (v Values) Bool(k string) bool { b, _ := v[k].(bool); return b }
func (v Values) Clone() Values {
	n := Values{}
	for k, x := range v {
		n[k] = x
	}
	return n
}

// Schema описывает поля как [группа, ключ, подпись, варианты/диапазон].
// Camera/Screen/Audio строят форму, Client задаёт видимость на приёмнике.
type Schema struct {
	Defaults              map[string]Values `json:"defaults"`
	Camera, Screen, Audio [][]any
	Client                map[string][]string
}

var schema Schema

const panelSettingsFile = "settings_go_panel.json"

type panelPreferences struct {
	Role          string          `json:"role"`
	Tab           int             `json:"tab"`
	Advanced      map[string]bool `json:"advanced,omitempty"`
	SenderPreview map[string]bool `json:"sender_preview,omitempty"`
}

func loadPanelPreferences(name string) panelPreferences {
	var p panelPreferences
	if data, err := os.ReadFile(name); err == nil {
		if json.Unmarshal(data, &p) != nil {
			return panelPreferences{}
		}
	}
	return p
}

func init() {
	b, _ := schemaFS.ReadFile("settings_schema.json")
	if err := json.Unmarshal(b, &schema); err != nil {
		panic(err)
	}
}

// Config — снимок применённых параметров. Screen содержит также параметры звука.
// Host общий для обоих потоков; роли имеют отдельные файлы профилей.
type Config struct {
	Role            string                    `json:"role"`
	Host            string                    `json:"host"`
	Camera          Values                    `json:"camera"`
	Screen          Values                    `json:"screen"`
	Cameras         []cameraChannel           `json:"cameras,omitempty"`
	CameraName      string                    `json:"camera_name,omitempty"`
	ReceiverSources map[string]receiverSource `json:"receiver_sources,omitempty"`
	ReceiverCameras []receiverSource          `json:"receiver_cameras,omitempty"`
}

type receiverSource struct {
	Endpoint netip.AddrPort `json:"endpoint"`
	Name     string         `json:"name,omitempty"`
}

const receiverCameraPrefix = "receiver-camera:"

func (s receiverSource) cameraKey() string { return receiverCameraPrefix + s.Endpoint.String() }

// Preview 8 profiles already have a primary selected camera. Keep it in that
// field so older versions can still open the same profile.
func (c Config) selectedReceiverCameras() []receiverSource {
	var selected []receiverSource
	if source, ok := c.ReceiverSources["camera"]; ok {
		selected = append(selected, source)
	}
	return append(selected, c.ReceiverCameras...)
}

func (c Config) receiverCamera(key string) (receiverSource, bool) {
	if source, ok := c.ReceiverSources["camera"]; ok && source.cameraKey() == key {
		return source, true
	}
	for _, source := range c.ReceiverCameras {
		if source.cameraKey() == key {
			return source, true
		}
	}
	return receiverSource{}, false
}

func (c Config) receiverEndpoint(kind string) (string, int) {
	if source, ok := c.ReceiverSources[kind]; ok && source.Endpoint.IsValid() {
		return source.Endpoint.Addr().String(), int(source.Endpoint.Port())
	}
	return c.Host, c.receiverPort(kind)
}

func (c Config) receiverPort(kind string) int {
	switch kind {
	case "camera":
		return c.Camera.Int("camera_port")
	case "screen":
		return c.Screen.Int("video_port")
	default:
		return c.Screen.Int("audio_port")
	}
}

func (c Config) receiverEndpointChanged(next Config, kind string) bool {
	host, port := c.receiverEndpoint(kind)
	nextHost, nextPort := next.receiverEndpoint(kind)
	return host != nextHost || port != nextPort
}

func (c Config) primaryCameraName() string {
	if name := strings.TrimSpace(c.CameraName); name != "" {
		return name
	}
	return "Камера 1"
}

type cameraChannel struct {
	Name   string `json:"name"`
	Port   int    `json:"port"`
	Camera Values `json:"camera"`
}

func (c cameraChannel) key() string { return "camera:" + strconv.Itoa(c.Port) }

func (c Config) extraCamera(key string) (cameraChannel, bool) {
	for _, channel := range c.Cameras {
		if channel.key() == key {
			return channel, true
		}
	}
	return cameraChannel{}, false
}

func (c Config) streamKeys() []string {
	if c.Role == "client" {
		if selected := c.selectedReceiverCameras(); len(selected) != 0 {
			keys := make([]string, 0, len(selected)+1)
			for _, source := range selected {
				keys = append(keys, source.cameraKey())
			}
			return append(keys, "screen")
		}
	}
	keys := []string{"camera", "screen"}
	if c.Role == "server" {
		for _, channel := range c.Cameras {
			keys = append(keys, channel.key())
		}
	}
	return keys
}

func (c Config) clone() Config {
	c.Camera, c.Screen = c.Camera.Clone(), c.Screen.Clone()
	c.Cameras = append([]cameraChannel(nil), c.Cameras...)
	c.ReceiverSources = maps.Clone(c.ReceiverSources)
	c.ReceiverCameras = slices.Clone(c.ReceiverCameras)
	for i := range c.Cameras {
		c.Cameras[i].Camera = c.Cameras[i].Camera.Clone()
	}
	return c
}

func defaultConfig(role string) Config {
	return Config{Role: role, Camera: schema.Defaults["camera"].Clone(), Screen: schema.Defaults["screen"].Clone()}
}

// loadConfig накладывает старые профили на defaults, затем Go-профиль поверх них.
// Неизвестные старые ключи отбрасываются; исходные Python-файлы не изменяются.
func loadConfig(role string) Config {
	c := defaultConfig(role)
	// Legacy values supply defaults for keys absent from a Go profile.
	for kind, target := range map[string]Values{"camera": c.Camera, "screen": c.Screen} {
		name := "settings_" + role + ".json"
		if kind == "screen" {
			name = "settings_screen_" + role + ".json"
		}
		if b, e := os.ReadFile(name); e == nil {
			var m Values
			if json.Unmarshal(b, &m) == nil {
				for k, v := range m {
					if _, ok := target[k]; ok {
						target[k] = v
					}
				}
			}
		}
	}
	if b, e := os.ReadFile("settings_go_" + role + ".json"); e == nil {
		var saved struct {
			Host            *string
			Camera, Screen  Values
			Cameras         []cameraChannel
			CameraName      string                    `json:"camera_name"`
			ReceiverSources map[string]receiverSource `json:"receiver_sources"`
			ReceiverCameras []receiverSource          `json:"receiver_cameras"`
		}
		if json.Unmarshal(b, &saved) == nil {
			maps.Copy(c.Camera, saved.Camera)
			maps.Copy(c.Screen, saved.Screen)
			c.Cameras = saved.Cameras
			c.CameraName = saved.CameraName
			c.ReceiverSources = saved.ReceiverSources
			c.ReceiverCameras = saved.ReceiverCameras
			for i := range c.Cameras {
				values := schema.Defaults["camera"].Clone()
				maps.Copy(values, c.Cameras[i].Camera)
				c.Cameras[i].Camera = values
			}
			if saved.Host != nil {
				c.Host = *saved.Host
				return c
			}
		}
	}
	if c.Host == "" {
		c.Host = c.Camera.Str("host")
		if c.Host == "" {
			c.Host = c.Screen.Str("host")
		}
	}
	return c
}

// saveConfig пишет временный файл рядом с профилем и затем переименовывает его.
// Это уменьшает риск частичного JSON, но не является гарантией fsync/атомарности
// на любой файловой системе Windows; ошибки должен обрабатывать вызывающий код.
func saveConfig(c Config) error {
	return saveSettingsJSON("settings_go_"+c.Role+".json", c)
}

func saveSettingsJSON(name string, value any) error {
	data, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".*.tmp")
	if e != nil {
		return e
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(temp, name)
}

// validate проверяет диапазоны, варианты, роль, host и конфликт портов.
// На приёмнике параметры захвата намеренно пропускаются. Доступность устройства
// и все сочетания полей здесь не проверяются — это проверяют рабочие модули.
func validate(c Config) error {
	if c.Role != "client" && c.Role != "server" && c.Role != "local" {
		return fmt.Errorf("неизвестный режим")
	}
	if strings.ContainsAny(c.Host, " \r\n\t") || len(c.Host) > 253 {
		return fmt.Errorf("неверный IP / имя передатчика")
	}
	for kind, v := range map[string]Values{"camera": c.Camera, "screen": c.Screen} {
		fs := schema.Camera
		if kind == "screen" {
			fs = append(append([][]any{}, schema.Screen...), schema.Audio...)
		}
		for _, f := range fs {
			k := f[1].(string)
			if c.Role == "client" && !contains(schema.Client[kind], k) ||
				c.Role != "client" && k == "camera_port" {
				continue
			}
			if err := validateField(v, f); err != nil {
				return err
			}
		}
	}
	vp, ap := c.Screen.Int("video_port"), c.Screen.Int("audio_port")
	if c.Role == "client" {
		if _, err := strconv.Atoi(c.Camera.Str("camera_port")); err != nil {
			return fmt.Errorf("UDP-порт камеры: нужно целое число")
		}
		endpoints := make(map[string]bool, 3)
		for _, kind := range []string{"camera", "screen", "audio"} {
			if source, ok := c.ReceiverSources[kind]; ok &&
				(!source.Endpoint.Addr().Is4() || source.Endpoint.Addr().IsUnspecified() || source.Endpoint.Port() == 0) {
				return fmt.Errorf("недопустимый адрес источника")
			}
			host, port := c.receiverEndpoint(kind)
			key := strings.ToLower(host) + ":" + strconv.Itoa(port)
			if endpoints[key] {
				return fmt.Errorf("потоки одного передатчика требуют разных UDP-портов")
			}
			endpoints[key] = true
		}
		for _, camera := range c.ReceiverCameras {
			if !camera.Endpoint.Addr().Is4() || camera.Endpoint.Addr().IsUnspecified() || camera.Endpoint.Port() == 0 {
				return fmt.Errorf("недопустимый адрес камеры")
			}
			key := camera.Endpoint.String()
			if endpoints[key] {
				return fmt.Errorf("источник %s уже выбран", key)
			}
			endpoints[key] = true
		}
	} else if vp == ap || vp == 5001 || ap == 5001 {
		return fmt.Errorf("камера, экран и звук требуют разных UDP-портов")
	}
	if c.Role == "server" {
		used := map[int]bool{vp: true, ap: true}
		devices := map[string]bool{c.Camera.Str("camera"): true}
		for _, channel := range c.Cameras {
			if channel.Port < firstExtraCameraPort || channel.Port > lastExtraCameraPort || used[channel.Port] {
				return fmt.Errorf("%s: конфликт UDP-порта %d", channel.Name, channel.Port)
			}
			if device := channel.Camera.Str("camera"); device != "" {
				if devices[device] {
					return fmt.Errorf("%s: выберите отдельное устройство камеры", channel.Name)
				}
				devices[device] = true
			}
			for _, field := range schema.Camera {
				if err := validateField(channel.Camera, field); err != nil {
					return fmt.Errorf("%s: %w", channel.Name, err)
				}
			}
			used[channel.Port] = true
		}
	}
	return nil
}

func validateField(v Values, field []any) error {
	key, label := field[1].(string), field[2].(string)
	opts, ok := field[3].([]any)
	if !ok || len(opts) == 0 {
		return nil
	}
	if lo, number := opts[0].(float64); number && len(opts) == 2 {
		if text, ok := v[key].(string); ok {
			if _, err := strconv.ParseFloat(text, 64); err != nil {
				return fmt.Errorf("%s: нужно число", label)
			}
		}
		n, hi := v.Num(key), opts[1].(float64)
		if math.IsNaN(n) || math.IsInf(n, 0) || n < lo || n > hi {
			return fmt.Errorf("%s: допустимо %g–%g", label, lo, hi)
		}
	} else if !containsAny(opts, v.Str(key)) {
		return fmt.Errorf("%s: выберите значение из списка", label)
	}
	return nil
}
func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
func containsAny(a []any, s string) bool {
	for _, x := range a {
		if fmt.Sprint(x) == s {
			return true
		}
	}
	return false
}

// ConfigStore публикует конфигурацию через atomic.Value. Set копирует карты.
// Полученные Get карты считаются неизменяемыми: перед правкой нужен Clone.
// Clone поверхностный; вложенные изменяемые значения потребуют другой схемы.
type ConfigStore struct{ value atomic.Value }

func newStore(c Config) *ConfigStore { s := &ConfigStore{}; s.Set(c); return s }
func (s *ConfigStore) Get() Config   { return s.value.Load().(Config) }
func (s *ConfigStore) Set(c Config) {
	s.value.Store(c.clone())
}

// executableDirectory делает профили, снимки и журналы относительными к EXE.
// Это выбор переносимого приложения; библиотечный компонент не должен менять cwd.
func executableDirectory() {
	if p, e := os.Executable(); e == nil {
		_ = os.Chdir(filepath.Dir(p))
	}
}
