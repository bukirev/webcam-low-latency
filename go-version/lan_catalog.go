package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const lanCatalogPort = 51040

type lanFeed struct {
	Kind string `json:"kind"`
	Port uint16 `json:"port"`
	Name string `json:"name"`
}

type lanCatalog struct {
	conn     *net.UDPConn
	hostname string
	// Only the panel publishes snapshots; the UDP worker never reads its widgets.
	feeds atomic.Pointer[[]lanFeed]
}

func openLANCatalog(port int) (*lanCatalog, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if err != nil {
		return nil, err
	}
	service := &lanCatalog{conn: conn, hostname: localDiscoveryName()}
	go service.serve()
	return service, nil
}

func (s *lanCatalog) serve() {
	var buf [256]byte
	for {
		n, peer, err := s.conn.ReadFromUDPAddrPort(buf[:])
		if err != nil {
			return
		}
		token, ok := strings.CutPrefix(string(buf[:n]), "VB2LINK FIND ")
		if !ok || len(token) != 16 || strings.ContainsFunc(token, func(r rune) bool {
			return !strings.ContainsRune("0123456789abcdef", r)
		}) {
			continue
		}
		feeds := s.feeds.Load()
		if feeds == nil || len(*feeds) == 0 {
			continue
		}
		var packet bytes.Buffer
		packet.WriteString("VB2LINK HERE " + token + " ")
		enc := json.NewEncoder(&packet)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(*feeds); err == nil && packet.Len() <= 1400 {
			_, _ = s.conn.WriteToUDPAddrPort(packet.Bytes(), peer)
		}
	}
}

func searchCatalog(port uint16, ips []netip.Addr) map[string][]lanSender {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil
	}
	defer conn.Close()
	_ = enableBroadcast(conn)
	_ = conn.SetDeadline(time.Now().Add(550 * time.Millisecond))
	token := fmt.Sprintf("%016x", sessionID())
	for _, ip := range ips {
		_, _ = conn.WriteToUDPAddrPort([]byte("VB2LINK FIND "+token), netip.AddrPortFrom(ip, port))
	}
	found := make(map[string][]lanSender)
	var buf [1400]byte
	for replies := 0; replies < discoveryLimit; replies++ {
		n, from, err := conn.ReadFromUDPAddrPort(buf[:])
		if err != nil {
			break
		}
		ip := from.Addr().Unmap()
		data, ok := strings.CutPrefix(string(buf[:n]), "VB2LINK HERE "+token+" ")
		if !ok || from.Port() != port || !ip.Is4() || ip.IsUnspecified() {
			continue
		}
		var feeds []lanFeed
		if json.Unmarshal([]byte(data), &feeds) != nil || len(feeds) > 10 {
			continue
		}
		for _, feed := range feeds {
			if (feed.Kind == "camera" || feed.Kind == "screen" || feed.Kind == "audio") && feed.Port != 0 {
				found[feed.Kind] = append(found[feed.Kind], lanSender{ip: ip, port: feed.Port, name: discoveryName(feed.Name)})
			}
		}
	}
	return found
}

func scanReceiverSources(c Config) map[string][]lanSender {
	ips := lanDiscoveryIPs()
	if ip, err := netip.ParseAddr(c.Host); err == nil && ip.Is4() && !slices.Contains(ips, ip) {
		ips = append(ips, ip)
	}
	var wg sync.WaitGroup
	var cameras, screens, audio []lanSender
	var catalog map[string][]lanSender
	wg.Go(func() {
		cameras, _ = searchCameras(c.receiverPort("camera"), ips)
	})
	wg.Go(func() { screens = searchReceiverFeed(c, "screen", "WS40", 5002, ips) })
	wg.Go(func() { audio = searchReceiverFeed(c, "audio", "WA40", 5003, ips) })
	wg.Go(func() { catalog = searchCatalog(lanCatalogPort, ips) })
	wg.Wait()
	return map[string][]lanSender{
		"camera": mergeReceiverSources(cameras, catalog["camera"]),
		"screen": mergeReceiverSources(screens, catalog["screen"]),
		"audio":  mergeReceiverSources(audio, catalog["audio"]),
	}
}

func searchReceiverFeed(c Config, kind, magic string, defaultPort uint16, ips []netip.Addr) []lanSender {
	ports := []uint16{defaultPort}
	_, selected := c.receiverEndpoint(kind)
	for _, port := range []int{c.receiverPort(kind), selected} {
		if port > 0 && port <= 65535 && !slices.Contains(ports, uint16(port)) {
			ports = append(ports, uint16(port))
		}
	}
	senders, _ := searchLANPorts(magic, ports, ips)
	return senders
}

func mergeReceiverSources(legacy, catalog []lanSender) []lanSender {
	result := make(map[netip.AddrPort]lanSender)
	for _, list := range [][]lanSender{legacy, catalog} {
		for _, sender := range list {
			result[netip.AddrPortFrom(sender.ip, sender.port)] = sender
		}
	}
	merged := make([]lanSender, 0, len(result))
	for _, sender := range result {
		merged = append(merged, sender)
	}
	sortLANSources(merged)
	return merged[:min(len(merged), discoveryLimit)]
}
