package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProfileRemembersEmptyHostAndSeparateRoles(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("settings_client.json", []byte(`{"host":"192.168.1.40"}`), 0600); err != nil {
		t.Fatal(err)
	}
	client := loadConfig("client")
	if client.Host != "192.168.1.40" {
		t.Fatalf("legacy migration lost host: %q", client.Host)
	}
	if err := saveConfig(client); err != nil {
		t.Fatal(err)
	}
	client.Host = ""
	client.Screen["volume"] = float64(45)
	if err := saveConfig(client); err != nil {
		t.Fatal(err)
	}
	sender := defaultConfig("server")
	sender.Screen["max_width"] = "1512"
	if err := saveConfig(sender); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig("client"); got.Host != "" || got.Screen.Num("volume") != 45 {
		t.Fatalf("client profile did not restore empty host and volume: %q, %v", got.Host, got.Screen["volume"])
	}
	if got := loadConfig("server"); got.Host != "" || got.Screen.Str("max_width") != "1512" {
		t.Fatalf("sender profile did not restore custom width: %q, %v", got.Host, got.Screen["max_width"])
	}
}

func TestPanelPreferencesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), panelSettingsFile)
	want := panelPreferences{
		Role: "server", Tab: 1,
		Advanced: map[string]bool{"Экран": true}, SenderPreview: map[string]bool{"camera": true},
	}
	if err := saveSettingsJSON(path, want); err != nil {
		t.Fatal(err)
	}
	if got := loadPanelPreferences(path); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored preferences = %#v, want %#v", got, want)
	}
}

func TestReceiverCanSelectAdditionalCameraPort(t *testing.T) {
	cfg := defaultConfig("client")
	cfg.Camera["camera_port"] = "5007"
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"0", "5002", "5007.5", "70000"} {
		cfg.Camera["camera_port"] = invalid
		if err := validate(cfg); err == nil {
			t.Errorf("accepted invalid receiver camera port %q", invalid)
		}
	}
	cfg.Role = "server"
	if err := validate(cfg); err != nil {
		t.Fatalf("sender rejected receiver-only settings: %v", err)
	}
}

func TestReceiverSourcesMayUseDifferentHostsOnTheSamePort(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := defaultConfig("client")
	cfg.Host = "192.168.1.31"
	cfg.ReceiverSources = map[string]receiverSource{
		"camera": {netip.MustParseAddrPort("192.168.1.32:5002"), "USB"},
		"screen": {netip.MustParseAddrPort("192.168.1.33:5002"), "Screen"},
	}
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	next := loadConfig("client")
	if !reflect.DeepEqual(next.ReceiverSources, cfg.ReceiverSources) || cfg.receiverEndpointChanged(next, "screen") {
		t.Fatalf("restored receiver sources: %+v", next.ReceiverSources)
	}
	if host, port := next.receiverEndpoint("audio"); host != cfg.Host || port != 5003 {
		t.Fatalf("manual audio fallback: %s:%d", host, port)
	}
	next.ReceiverSources["screen"] = cfg.ReceiverSources["camera"]
	if !cfg.receiverEndpointChanged(next, "screen") || validate(next) == nil {
		t.Fatal("identical endpoint for two feeds was accepted")
	}
}

func TestMultipleCameraProfile(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := defaultConfig("server")
	extra := cameraChannel{"USB", firstExtraCameraPort, schema.Defaults["camera"].Clone()}
	extra.Camera["camera"] = "usb-camera-id"
	extra.Camera["fps"] = "25"
	cfg.Cameras = []cameraChannel{extra}
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	restored := loadConfig("server")
	if !reflect.DeepEqual(restored.Cameras, cfg.Cameras) {
		t.Fatalf("restored camera channels = %#v", restored.Cameras)
	}
	snapshot := newStore(restored)
	restored.Cameras[0].Camera["fps"] = "15"
	if snapshot.Get().Cameras[0].Camera.Str("fps") != "25" {
		t.Fatal("published camera settings share the draft")
	}
	restored.Screen["video_port"] = firstExtraCameraPort
	if err := validate(restored); err == nil {
		t.Fatal("accepted camera and screen on the same port")
	}
	restored.Screen["video_port"] = cfg.Screen["video_port"]
	restored.Cameras[0].Camera["camera"] = restored.Camera["camera"]
	if err := validate(restored); err == nil {
		t.Fatal("accepted the same physical device for both feeds")
	}
}
