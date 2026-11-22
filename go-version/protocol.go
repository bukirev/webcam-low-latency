package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const chunkSize = 1300
const videoHeader = 20

type Latest[T any] struct{ ch chan T }

func newLatest[T any]() *Latest[T] { return &Latest[T]{make(chan T, 1)} }
func (l *Latest[T]) Put(v T) {
	select {
	case l.ch <- v:
		return
	default:
	}
	select {
	case <-l.ch:
	default:
	}
	select {
	case l.ch <- v:
	default:
	}
}

type Status struct {
	mu   sync.Mutex
	text string
}

func (s *Status) Set(t string) { s.mu.Lock(); s.text = t; s.mu.Unlock() }
func (s *Status) Get() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.text }
func newer(a, b uint32) bool   { return a != b && uint32(a-b) < 0x80000000 }
func sessionID() uint64        { var b [8]byte; _, _ = rand.Read(b[:]); return binary.BigEndian.Uint64(b[:]) }
func fragments(magic string, session uint64, seq uint32, data []byte, send func([]byte) error) error {
	n := (len(data) + chunkSize - 1) / chunkSize
	if n == 0 || n > 4096 {
		return fmt.Errorf("слишком большой JPEG")
	}
	var packet [videoHeader + chunkSize]byte
	copy(packet[:4], magic)
	binary.BigEndian.PutUint64(packet[4:12], session)
	binary.BigEndian.PutUint32(packet[12:16], seq)
	binary.BigEndian.PutUint16(packet[18:20], uint16(n))
	for i := 0; i < n; i++ {
		binary.BigEndian.PutUint16(packet[16:18], uint16(i))
		end := min(len(data), (i+1)*chunkSize)
		size := copy(packet[20:], data[i*chunkSize:end])
		if err := send(packet[:20+size]); err != nil {
			return err
		}
	}
	return nil
}

type partialFrame struct {
	at         time.Time
	parts      [][]byte
	have, size int
}
type Assembler struct {
	magic   string
	frames  map[uint32]*partialFrame
	last    uint32
	hasLast bool
}

func newAssembler(magic string) *Assembler {
	return &Assembler{magic: magic, frames: map[uint32]*partialFrame{}}
}
func (a *Assembler) Push(p []byte, now time.Time) ([]byte, uint32) {
	if len(p) <= 20 || len(p) > 20+chunkSize || string(p[:4]) != a.magic {
		return nil, 0
	}
	seq := binary.BigEndian.Uint32(p[12:16])
	i, n := int(binary.BigEndian.Uint16(p[16:18])), int(binary.BigEndian.Uint16(p[18:20]))
	if n < 1 || n > 4096 || i >= n || a.hasLast && !newer(seq, a.last) {
		return nil, 0
	}
	for k, v := range a.frames {
		if now.Sub(v.at) > 150*time.Millisecond {
			delete(a.frames, k)
		}
	}
	f := a.frames[seq]
	if f == nil {
		if len(a.frames) >= 8 {
			var oldest uint32
			var t time.Time
			for k, v := range a.frames {
				if t.IsZero() || v.at.Before(t) {
					t = v.at
					oldest = k
				}
			}
			delete(a.frames, oldest)
		}
		f = &partialFrame{at: now, parts: make([][]byte, n)}
		a.frames[seq] = f
	}
	if len(f.parts) != n || f.parts[i] != nil {
		return nil, 0
	}
	f.parts[i] = append([]byte(nil), p[20:]...)
	f.have++
	f.size += len(p) - 20
	if f.have != n {
		return nil, 0
	}
	data := make([]byte, 0, f.size)
	for _, part := range f.parts {
		data = append(data, part...)
	}
	delete(a.frames, seq)
	a.last = seq
	a.hasLast = true
	return data, seq
}
func receiveVideo(ctx context.Context, magic string, port int, host string, out *Latest[[]byte], status *Status) {
	for ctx.Err() == nil {
		err := receiveVideoOnce(ctx, magic, port, host, out, status)
		if ctx.Err() != nil {
			return
		}
		status.Set("Ошибка UDP: " + fmt.Sprint(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func receiveVideoOnce(ctx context.Context, magic string, port int, host string, out *Latest[[]byte], status *Status) error {
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{})
	if e != nil {
		return e
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(4 << 20)
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
	target := "255.255.255.255"
	if host != "" {
		target = host
	}
	addr, e := net.ResolveUDPAddr("udp4", net.JoinHostPort(target, strconv.Itoa(port)))
	if e != nil {
		return e
	}
	allowed := net.IP(nil)
	if host != "" {
		allowed = addr.IP
	}
	a := newAssembler(magic)
	source := ""
	var sess uint64
	var seen, ping, stamp time.Time
	var bytes int
	count := 0
	buf := make([]byte, 1500)
	for ctx.Err() == nil {
		now := time.Now()
		if now.Sub(ping) >= time.Second {
			dest := *addr
			if source != "" {
				dest.IP = net.ParseIP(source)
			}
			_, _ = conn.WriteToUDP([]byte(fmt.Sprintf("%s SUB %d", magic, conn.LocalAddr().(*net.UDPAddr).Port)), &dest)
			ping = now
		}
		if source != "" && now.Sub(seen) > 2*time.Second {
			source = ""
			a = newAssembler(magic)
			out.Put(nil)
		}
		_ = conn.SetReadDeadline(now.Add(100 * time.Millisecond))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				return err
			}
		} else if n > 20 && string(buf[:4]) == magic && (allowed == nil || from.IP.Equal(allowed)) {
			session := binary.BigEndian.Uint64(buf[4:12])
			if source == "" {
				source = from.IP.String()
				sess = session
				a = newAssembler(magic)
			}
			if source == from.IP.String() && sess == session {
				seen = now
				bytes += n
				if data, _ := a.Push(buf[:n], now); data != nil {
					out.Put(data)
					count++
				}
			}
		}
		if now.Sub(stamp) >= time.Second {
			if source == "" {
				status.Set("Ожидание передатчика…")
			} else {
				status.Set(fmt.Sprintf("%s | %d FPS | %.1f Мбит/с", source, count, float64(bytes)*8/1e6))
			}
			count = 0
			bytes = 0
			stamp = now
		}
	}
	return ctx.Err()
}

type VideoSender struct {
	input   *Latest[[]byte]
	status  *Status
	clients sync.Map
	session uint64
}

func newVideoSender(status *Status) *VideoSender {
	return &VideoSender{input: newLatest[[]byte](), status: status, session: sessionID()}
}
func (s *VideoSender) Run(ctx context.Context, magic string, port int) {
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if e != nil {
		s.status.Set(e.Error())
		return
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	go func() {
		buf := make([]byte, 256)
		for {
			n, addr, e := conn.ReadFromUDP(buf)
			if e != nil {
				return
			}
			prefix := magic + " SUB "
			if !strings.HasPrefix(string(buf[:n]), prefix) {
				continue
			}
			p, e := strconv.Atoi(string(buf[len(prefix):n]))
			if e == nil && p > 0 && p < 65536 {
				s.clients.Store(net.JoinHostPort(addr.IP.String(), strconv.Itoa(p)), time.Now())
			}
		}
	}()
	var seq uint32
	var bytes int
	stamp := time.Now()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case jpg := <-s.input.ch:
			seq++
			count := 0
			now := time.Now()
			s.clients.Range(func(key, value any) bool {
				if now.Sub(value.(time.Time)) > 5*time.Second {
					s.clients.Delete(key)
					return true
				}
				count++
				to, _ := net.ResolveUDPAddr("udp4", key.(string))
				_ = fragments(magic, s.session, seq, jpg, func(p []byte) error {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					_ = conn.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
					n, e := conn.WriteToUDP(p, to)
					bytes += n
					return e
				})
				return true
			})
			if now.Sub(stamp) >= time.Second {
				s.status.Set(fmt.Sprintf("%d клиентов | %.1f Мбит/с", count, float64(bytes)*8/now.Sub(stamp).Seconds()/1e6))
				stamp = now
				bytes = 0
			}
		}
	}
}
