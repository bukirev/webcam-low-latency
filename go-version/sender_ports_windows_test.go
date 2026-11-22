package main

import (
	"net"
	"strings"
	"testing"
)

func TestSenderReleasesVideoPortWhenAudioPortIsBusy(t *testing.T) {
	occupied, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	audioPort := occupied.LocalAddr().(*net.UDPAddr).Port
	videoPort := freeUDPPort(t)
	cfg := defaultConfig("server")
	cfg.Screen["audio_port"] = audioPort
	cfg.Screen["video_port"] = videoPort
	video, audio, err := reserveSenderPorts("screen", cfg)
	if video != nil {
		video.Close()
	}
	if audio != nil {
		audio.Close()
	}
	if video != nil || audio != nil || err == nil || !strings.Contains(err.Error(), "другой передатчик") {
		t.Fatalf("expected a clear bind error and no usable sockets, got %v", err)
	}
	reopened, err := net.ListenUDP("udp4", &net.UDPAddr{Port: videoPort})
	if err != nil {
		t.Fatalf("video port leaked: %v", err)
	}
	reopened.Close()
}
