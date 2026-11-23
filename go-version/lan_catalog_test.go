package main

import (
	"net"
	"net/netip"
	"slices"
	"testing"
)

func TestLANCatalogDiscoversCustomPorts(t *testing.T) {
	service, err := openLANCatalog(0)
	if err != nil {
		t.Fatal(err)
	}
	defer service.conn.Close()
	feeds := []lanFeed{{"camera", 5004, "USB"}, {"screen", 59001, "Screen"}, {"audio", 59002, "Audio"}}
	service.feeds.Store(&feeds)
	loopback := netip.MustParseAddr("127.0.0.1")
	port := service.conn.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	found := searchCatalog(port, []netip.Addr{loopback})
	for _, feed := range feeds {
		expected := []lanSender{{ip: loopback, name: feed.Name, port: feed.Port}}
		if !slices.Equal(found[feed.Kind], expected) {
			t.Errorf("%s discovery = %v, want %v", feed.Kind, found[feed.Kind], expected)
		}
	}
	merged := mergeReceiverSources([]lanSender{{ip: loopback, name: "Legacy label", port: 59001}}, found["screen"])
	if !slices.Equal(merged, found["screen"]) {
		t.Fatalf("catalog did not replace duplicate endpoint: %v", merged)
	}
}
