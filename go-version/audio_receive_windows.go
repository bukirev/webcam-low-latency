package main

import (
	"context"
	"fmt"
	"net"
	"time"
)

// receiveAudioPackets проверяет source/session/seq и держит до 64 пакетов.
// При перегрузке удаляет старый пакет. Это не jitter reorder buffer: опоздавший
// seq отбрасывается, позиции пропусков по timestamps протокол не восстанавливает.
func receiveAudioPackets(ctx context.Context, host string, port int, out chan AudioPacket, status *Status) {
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{})
	if e != nil {
		status.Set(e.Error())
		return
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(128 << 10)
	_ = enableBroadcast(conn)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	addr, e := resolveSender(ctx, host, port)
	if e != nil {
		status.Set(e.Error())
		return
	}
	var ping, seen time.Time
	source := ""
	var session uint64
	var last uint32
	hasLast := false
	buf := make([]byte, 1500)
	for ctx.Err() == nil {
		now := time.Now()
		if now.Sub(ping) >= time.Second {
			dest := *addr
			if source != "" {
				dest.IP = net.ParseIP(source)
			}
			_, _ = conn.WriteToUDP([]byte(fmt.Sprintf("WA40 SUB %d", conn.LocalAddr().(*net.UDPAddr).Port)), &dest)
			ping = now
		}
		if source != "" && now.Sub(seen) > 2*time.Second {
			source = ""
			hasLast = false
		}
		_ = conn.SetReadDeadline(now.Add(100 * time.Millisecond))
		n, from, e := conn.ReadFromUDP(buf)
		if e != nil {
			if ne, ok := e.(net.Error); !ok || !ne.Timeout() {
				status.Set(e.Error())
				return
			}
			continue
		}
		a, e := parseAudio(buf[:n])
		if e != nil || host != "" && !from.IP.Equal(addr.IP) {
			continue
		}
		if source != "" && (source != from.IP.String() || session != a.Session) {
			continue
		}
		if source == "" {
			source = from.IP.String()
			session = a.Session
			hasLast = false
		}
		if hasLast && !newer(a.Seq, last) {
			continue
		}
		last = a.Seq
		hasLast = true
		seen = now
		select {
		case out <- a:
		default:
			select {
			case <-out:
			default:
			}
			select {
			case out <- a:
			default:
			}
		}
	}
}
