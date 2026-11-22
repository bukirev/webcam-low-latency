package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestVideoAdaptationHysteresis(t *testing.T) {
	var a videoAdaptation
	a.observe(1, 14)
	if a.level != 0 {
		t.Fatal("a single moderate loss changed the level")
	}
	a.observe(1, 18)
	a.observe(1, 40)
	if a.level != 2 {
		t.Fatalf("after sustained/severe loss, level = %d", a.level)
	}
	for range 10 {
		a.observe(1, -1)
	}
	for range 5 {
		a.observe(1, 1)
	}
	if a.level != 2 {
		t.Fatal("the sender restored quality before six healthy windows")
	}
	a.observe(1, 0)
	if a.level != 1 {
		t.Fatal("recovery must restore only one step")
	}
	a.observe(0, -1)
	if a.level != 0 {
		t.Fatal("a new subscriber should not inherit a departed subscriber's level")
	}
	if width, quality := budgetJPEG(4, 1920, 0, 80, true); width >= 1920 || quality >= 80 {
		t.Fatalf("screen budget did not decrease: width=%d quality=%d", width, quality)
	}
	if width, quality := budgetJPEG(4, 320, 200, 40, true); width != 200 || quality != 40 {
		t.Fatalf("budget raised a manually configured limit: width=%d quality=%d", width, quality)
	}
	if width, quality := budgetJPEG(4, 1920, 0, 80, false); width != 0 || quality >= 80 {
		t.Fatalf("fixed dimensions or JPEG quality were not applied: width=%d quality=%d", width, quality)
	}
}

func TestReceiverReportsMissingVideoFrames(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := conn.LocalAddr().(*net.UDPAddr).Port
	go receiveVideo(ctx, "WS40", port, "127.0.0.1", newLatest[[]byte](), &Status{})
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	var buf [256]byte
	n, peer, err := conn.ReadFromUDP(buf[:])
	if err != nil || !strings.HasPrefix(string(buf[:n]), "WS40 SUB ") {
		t.Fatalf("subscription: %q %v", buf[:n], err)
	}
	const session = 101
	for seq := uint32(100); seq < 110; seq++ {
		if seq == 103 || seq == 106 || seq == 107 {
			continue
		}
		err := fragments("WS40", session, seq, []byte("frame"), func(p []byte) error {
			_, err := conn.WriteToUDP(p, peer)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for {
		n, _, err = conn.ReadFromUDP(buf[:])
		if err != nil {
			t.Fatal(err)
		}
		loss, ok := parseVideoFeedback(string(buf[:n]), "WS40", session)
		if !ok {
			continue
		}
		if loss != 30 {
			t.Fatalf("loss = %d%%", loss)
		}
		break
	}
}

func TestVideoSenderAdaptsOverLossyUDP(t *testing.T) {
	listen := func() *net.UDPConn {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	tx, relay := listen(), listen()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	sender := newVideoSender(&Status{})
	sender.auto = func() bool { return true }
	go sender.runOn(ctx, "WS40", tx)
	// Both control and media pass through the relay. Drop a whole frame on
	// every third sequence, while leaving SUB and STAT untouched.
	go func() {
		var client *net.UDPAddr
		var buf [1500]byte
		txAddr := tx.LocalAddr().(*net.UDPAddr)
		for {
			n, from, err := relay.ReadFromUDP(buf[:])
			if err != nil {
				return
			}
			packet := buf[:n]
			switch {
			case from.Port == txAddr.Port:
				if client != nil && validFragment(packet, "WS40") && binary.BigEndian.Uint32(packet[12:16])%3 != 0 {
					_, _ = relay.WriteToUDP(packet, client)
				}
			case strings.HasPrefix(string(packet), "WS40 SUB "):
				client = from
				sub := fmt.Appendf(nil, "WS40 SUB %d", relay.LocalAddr().(*net.UDPAddr).Port)
				_, _ = relay.WriteToUDP(sub, txAddr)
			case client != nil && from.Port == client.Port:
				_, _ = relay.WriteToUDP(packet, txAddr)
			}
		}
	}()
	go receiveVideo(ctx, "WS40", relay.LocalAddr().(*net.UDPAddr).Port, "127.0.0.1", newLatest[[]byte](), &Status{})
	ticks := time.NewTicker(time.Second / 30)
	defer ticks.Stop()
	for sender.currentLevel() == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("sender did not adapt after sustained loss")
		case <-ticks.C:
			sender.input.Put([]byte("frame"))
		}
	}
}
