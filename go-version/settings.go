package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

const version = "5.0 OBS preview 4"

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

func init() {
	b, _ := schemaFS.ReadFile("settings_schema.json")
	if err := json.Unmarshal(b, &schema); err != nil {
		panic(err)
	}
}

// Config — снимок применённых параметров. Screen содержит также параметры звука.
// Host общий для обоих потоков; роли имеют отдельные файлы профилей.
type Config struct {
	Role   string `json:"role"`
	Host   string `json:"host"`
	Camera Values `json:"camera"`
	Screen Values `json:"screen"`
}

func defaultConfig(role string) Config {
	return Config{role, "", schema.Defaults["camera"].Clone(), schema.Defaults["screen"].Clone()}
}

// loadConfig накладывает старые профили на defaults, затем Go-профиль поверх них.
// Неизвестные старые ключи отбрасываются; исходные Python-файлы не изменяются.
func loadConfig(role string) Config {
	c := defaultConfig(role)
	// Import previous Python settings only when there is no Go profile yet.
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
		_ = json.Unmarshal(b, &c)
	}
	c.Role = role
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
	data, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	name := "settings_go_" + c.Role + ".json"
	f, e := os.CreateTemp(".", name+".*.tmp")
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
			if c.Role == "client" && !contains(schema.Client[kind], k) {
				continue
			}
			if opts, ok := f[3].([]any); ok && len(opts) > 0 {
				if lo, ok := opts[0].(float64); ok && len(opts) == 2 {
					if text, ok := v[k].(string); ok {
						if _, err := strconv.ParseFloat(text, 64); err != nil {
							return fmt.Errorf("%s: нужно число", f[2])
						}
					}
					n := v.Num(k)
					hi := opts[1].(float64)
					if math.IsNaN(n) || math.IsInf(n, 0) || n < lo || n > hi {
						return fmt.Errorf("%s: допустимо %g–%g", f[2], lo, hi)
					}
				} else if !containsAny(opts, v.Str(k)) {
					return fmt.Errorf("%s: выберите значение из списка", f[2])
				}
			}
		}
	}
	vp, ap := c.Screen.Int("video_port"), c.Screen.Int("audio_port")
	if vp == ap || vp == 5001 || ap == 5001 {
		return fmt.Errorf("экран и звук требуют разных портов, кроме 5001")
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
	c.Camera = c.Camera.Clone()
	c.Screen = c.Screen.Clone()
	s.value.Store(c)
}

// executableDirectory делает профили, снимки и журналы относительными к EXE.
// Это выбор переносимого приложения; библиотечный компонент не должен менять cwd.
func executableDirectory() {
	if p, e := os.Executable(); e == nil {
		_ = os.Chdir(filepath.Dir(p))
	}
}
