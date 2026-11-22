package main

import (
	"net"
	"net/netip"
	"testing"
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
	if err != nil || len(got) != 2 || got[0] != (lanSender{first, "Camera A"}) || got[1] != (lanSender{second, "Camera B"}) {
		t.Fatalf("senders=%v, err=%v", got, err)
	}
	if replyDiscovery(nil, nil, []byte("WS40 FIND 0123456789abcdef"), "WC30", "camera") ||
		replyDiscovery(nil, nil, []byte("WC30 FIND 0123456789abcdeg"), "WC30", "camera") {
		t.Fatal("a request with the wrong stream type or nonce was accepted")
	}
}
