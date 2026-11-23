package main

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	discoveryLimit       = 64
	firstExtraCameraPort = 5004
	lastExtraCameraPort  = 5010
)

type lanSender struct {
	ip   netip.Addr
	name string
	port uint16
}

func discoveryName(name string) string {
	var label strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if !unicode.IsPrint(r) {
			continue
		}
		if label.Len()+len(string(r)) > 96 {
			break
		}
		label.WriteRune(r)
	}
	return label.String()
}

func localDiscoveryName() string {
	name, _ := os.Hostname()
	return discoveryName(name)
}

// A FIND response goes only to the requesting socket, and never creates a
// media subscription. Existing senders and receivers still use SUB unchanged.
func replyDiscovery(conn *net.UDPConn, peer *net.UDPAddr, packet []byte, magic, name string) bool {
	token, ok := strings.CutPrefix(string(packet), magic+" FIND ")
	if !ok || len(token) != 16 || strings.ContainsFunc(token, func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdef", r)
	}) {
		return false
	}
	_, _ = conn.WriteToUDP([]byte(magic+" HERE "+token+" "+name), peer)
	return true
}

func lanDiscoveryIPs() []netip.Addr {
	// Loopback includes a sender on this PC. Directed broadcasts also cover
	// interfaces that Windows does not choose for the limited broadcast.
	ips := []netip.Addr{netip.AddrFrom4([4]byte{127, 0, 0, 1}), netip.AddrFrom4([4]byte{255, 255, 255, 255})}
	interfaces, _ := net.Interfaces()
	for _, nic := range interfaces {
		if nic.Flags&(net.FlagUp|net.FlagBroadcast) != net.FlagUp|net.FlagBroadcast {
			continue
		}
		addresses, _ := nic.Addrs()
		for _, address := range addresses {
			subnet, ok := address.(*net.IPNet)
			if !ok || subnet.IP.To4() == nil || len(subnet.Mask) != net.IPv4len {
				continue
			}
			ones, _ := subnet.Mask.Size()
			if ones >= 31 {
				continue
			}
			var broadcast [4]byte
			for i, b := range subnet.IP.To4() {
				broadcast[i] = b | ^subnet.Mask[i]
			}
			ip := netip.AddrFrom4(broadcast)
			if !slices.Contains(ips, ip) {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

func searchLAN(magic string, port int, ips []netip.Addr) ([]lanSender, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid discovery port")
	}
	return searchLANPorts(magic, []uint16{uint16(port)}, ips)
}

func searchCameras(port int, ips []netip.Addr) ([]lanSender, error) {
	ports := []uint16{5001}
	for p := uint16(firstExtraCameraPort); p <= lastExtraCameraPort; p++ {
		ports = append(ports, p)
	}
	if port > 0 && port <= 65535 && !slices.Contains(ports, uint16(port)) {
		ports = append(ports, uint16(port))
	}
	return searchLANPorts("WC30", ports, ips)
}

func searchLANPorts(magic string, ports []uint16, ips []netip.Addr) ([]lanSender, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = enableBroadcast(conn)
	// Only the explicit Properties button calls this bounded scan.
	deadline := time.Now().Add(550 * time.Millisecond)
	_ = conn.SetDeadline(deadline)
	token := fmt.Sprintf("%016x", sessionID())
	probe := []byte(magic + " FIND " + token)
	prefix := magic + " HERE " + token + " "
	var sent bool
	for _, port := range ports {
		for _, ip := range ips {
			if _, err = conn.WriteToUDPAddrPort(probe, netip.AddrPortFrom(ip, port)); err == nil {
				sent = true
			}
		}
	}
	if !sent {
		return nil, fmt.Errorf("could not send discovery probe: %v", err)
	}
	found := make(map[netip.AddrPort]string)
	var buf [256]byte
	for len(found) < discoveryLimit {
		n, from, err := conn.ReadFromUDPAddrPort(buf[:])
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			break
		}
		if err != nil {
			return nil, err
		}
		name, ok := strings.CutPrefix(string(buf[:n]), prefix)
		ip := from.Addr().Unmap()
		if ok && slices.Contains(ports, from.Port()) && ip.Is4() && !ip.IsUnspecified() {
			found[netip.AddrPortFrom(ip, from.Port())] = discoveryName(name)
		}
	}
	senders := make([]lanSender, 0, len(found))
	for address, name := range found {
		senders = append(senders, lanSender{address.Addr(), name, address.Port()})
	}
	sortLANSources(senders)
	return senders, nil
}

func sortLANSources(senders []lanSender) {
	slices.SortFunc(senders, func(a, b lanSender) int {
		if byName := strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)); byName != 0 {
			return byName
		}
		return netip.AddrPortFrom(a.ip, a.port).Compare(netip.AddrPortFrom(b.ip, b.port))
	})
}
