package main

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestDiscoveryFindsMultipleSendersOnOnePort(t *testing.T) {
	first := netip.MustParseAddr("127.0.0.1")
	second := netip.MustParseAddr("127.0.0.2")
	listen := func(ip netip.Addr, port int) *net.UDPConn {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip.AsSlice(), Port: port})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	camera := listen(first, 0)
	port := camera.LocalAddr().(*net.UDPAddr).Port
	other := listen(second, port)
	for _, sender := range []struct {
		conn *net.UDPConn
		name string
	}{{camera, "Camera A"}, {other, "Camera B"}} {
		go func() {
			var buf [256]byte
			n, peer, err := sender.conn.ReadFromUDP(buf[:])
			if err == nil {
				replyDiscovery(sender.conn, peer, buf[:n], "WC30", sender.name)
			}
		}()
	}
	got, err := searchLAN("WC30", port, []netip.Addr{first, second})
	if err != nil || len(got) != 2 || got[0] != (lanSender{first, "Camera A", uint16(port)}) || got[1] != (lanSender{second, "Camera B", uint16(port)}) {
		t.Fatalf("senders=%v, err=%v", got, err)
	}
	if replyDiscovery(nil, nil, []byte("WS40 FIND 0123456789abcdef"), "WC30", "camera") ||
		replyDiscovery(nil, nil, []byte("WC30 FIND 0123456789abcdeg"), "WC30", "camera") {
		t.Fatal("a request with the wrong stream type or nonce was accepted")
	}
}

func TestTwoCameraFeedsOnOneIP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	local := netip.MustParseAddr("127.0.0.1")
	var ports []uint16
	var inputs []*VideoSender
	var outputs []*Latest[[]byte]
	for _, label := range []string{"Built-in", "USB"} {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: local.AsSlice()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		port := conn.LocalAddr().(*net.UDPAddr).AddrPort().Port()
		sender := newVideoSender(&Status{})
		sender.discoveryLabel = label
		go sender.runOn(ctx, "WC30", conn)
		receive := newLatest[[]byte]()
		go receiveVideo(ctx, "WC30", int(port), local.String(), receive, &Status{})
		ports, inputs, outputs = append(ports, port), append(inputs, sender), append(outputs, receive)
	}
	found, err := searchLANPorts("WC30", ports, []netip.Addr{local})
	if err != nil || len(found) != 2 ||
		found[0] != (lanSender{local, "Built-in", ports[0]}) ||
		found[1] != (lanSender{local, "USB", ports[1]}) {
		t.Fatalf("discovery=%v, err=%v", found, err)
	}
	want := [][]byte{bytes.Repeat([]byte{1}, 2000), bytes.Repeat([]byte{2}, 3000)}
	tick := time.NewTicker(15 * time.Millisecond)
	defer tick.Stop()
	first, second := outputs[0].ch, outputs[1].ch
	for first != nil || second != nil {
		select {
		case <-tick.C:
			for i, sender := range inputs {
				sender.input.Put(want[i])
			}
		case frame := <-first:
			if !bytes.Equal(frame, want[0]) {
				t.Fatal("first receiver delivered another camera's frame")
			}
			first = nil
		case frame := <-second:
			if !bytes.Equal(frame, want[1]) {
				t.Fatal("second receiver delivered another camera's frame")
			}
			second = nil
		case <-ctx.Done():
			t.Fatal("both feeds did not deliver a frame")
		}
	}
}
