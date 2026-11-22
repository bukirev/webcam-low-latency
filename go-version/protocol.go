package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Размеры — часть совместимого протокола, а не параметры GUI.
// Заголовок network byte order занимает 20 байт, полезная часть — до 1300.
const chunkSize = 1300
const videoHeader = 20

// Latest — неблокирующий почтовый ящик последнего видеокадра ёмкостью один.
// Переполнение выбрасывает старое значение. Put передаёт неизменяемый []byte;
// освобождение внешних ресурсов при вытеснении контейнер не выполняет.
type Latest[T any] struct{ ch chan T }

func newLatest[T any]() *Latest[T] { return &Latest[T]{make(chan T, 1)} }

// Put не ждёт потребителя и не закрывает канал. Сеанс завершается через context.
// Это политика свежести видео; для последовательных аудиосэмплов нужен FIFO.
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

// Status переносит короткое состояние из рабочих горутин в GUI под mutex.
type Status struct {
	mu   sync.Mutex
	text string
}

func (s *Status) Set(t string) { s.mu.Lock(); s.text = t; s.mu.Unlock() }
func (s *Status) Get() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.text }

// newer сравнивает uint32 по модулю 2^32, включая переход maxUint32 → 0.
// Разность в половину диапазона и больше считается старой/неоднозначной.
func newer(a, b uint32) bool { return a != b && uint32(a-b) < 0x80000000 }
func sessionID() uint64      { var b [8]byte; _, _ = rand.Read(b[:]); return binary.BigEndian.Uint64(b[:]) }

// fragments вызывает send синхронно: packet переиспользуется для всех фрагментов.
// Если send сохраняет срез для отложенной отправки, он обязан скопировать байты.
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

// Assembler собирает один источник/сеанс; переключение сеанса делает receiver.
// Объект принадлежит одной горутине, имеет предел частичных кадров и срок жизни.
type Assembler struct {
	magic   string
	frames  map[uint32]*partialFrame
	last    uint32
	hasLast bool
}

func newAssembler(magic string) *Assembler {
	return &Assembler{magic: magic, frames: map[uint32]*partialFrame{}}
}

func validFragment(p []byte, magic string) bool {
	if len(p) <= videoHeader || len(p) > videoHeader+chunkSize || string(p[:4]) != magic {
		return false
	}
	i, n := binary.BigEndian.Uint16(p[16:18]), binary.BigEndian.Uint16(p[18:20])
	return n != 0 && n <= 4096 && i < n
}

// Push валидирует пакет до выделения частей, игнорирует повторы и старые seq.
// Завершённый JPEG — новый независимый срез. Просрочка очищается на следующих
// пакетах; накопления истории для последующего воспроизведения здесь нет.
func (a *Assembler) Push(p []byte, now time.Time) ([]byte, uint32) {
	if !validFragment(p, a.magic) {
		return nil, 0
	}
	seq := binary.BigEndian.Uint32(p[12:16])
	i, n := int(binary.BigEndian.Uint16(p[16:18])), int(binary.BigEndian.Uint16(p[18:20]))
	if a.hasLast && !newer(seq, a.last) {
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

func resolveSender(ctx context.Context, host string, port int) (*net.UDPAddr, error) {
	if host == "" {
		return &net.UDPAddr{IP: net.IPv4bcast, Port: port}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("нет IPv4-адреса для %s", host)
	}
	return &net.UDPAddr{IP: net.IP(addresses[0].AsSlice()), Port: port}, nil
}

// receiveVideoOnce подписывается с эфемерного UDP-порта и закрепляет IP/session
// первого подходящего источника. При таймауте очистка публикует nil в decoder.
// ReadDeadline и закрытие сокета по context ограничивают ожидание остановки.
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
	addr, e := resolveSender(ctx, host, port)
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
	var health videoReportWindow
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
			if source != "" {
				if expected, complete := health.report(); expected > 0 {
					_, _ = conn.WriteToUDP(videoFeedback(magic, sess, expected, complete), &dest)
				}
			}
			ping = now
		}
		if source != "" && now.Sub(seen) > 2*time.Second {
			source = ""
			a = newAssembler(magic)
			health = videoReportWindow{}
			out.Put(nil)
		}
		_ = conn.SetReadDeadline(now.Add(100 * time.Millisecond))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
				return err
			}
		} else if validFragment(buf[:n], magic) && (allowed == nil || from.IP.Equal(allowed)) {
			session := binary.BigEndian.Uint64(buf[4:12])
			if source == "" {
				source = from.IP.String()
				sess = session
				a = newAssembler(magic)
			}
			if source == from.IP.String() && sess == session {
				seen = now
				bytes += n
				health.see(binary.BigEndian.Uint32(buf[12:16]))
				if data, _ := a.Push(buf[:n], now); data != nil {
					out.Put(data)
					count++
					health.complete++
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

// VideoSender отделяет захват от UDP-отправки почтовым ящиком. clients — sync.Map,
// потому что чтение SUB и отправка кадров выполняются в разных горутинах.
type VideoSender struct {
	input   *Latest[[]byte]
	status  *Status
	clients sync.Map
	session uint64
	auto    func() bool
	level   atomic.Int32
}

type videoPeer struct {
	lease        atomic.Int64
	lastFeedback atomic.Int64
	report       atomic.Pointer[peerReport]
}

type peerReport struct {
	loss int
	at   time.Time
}

func (s *VideoSender) currentLevel() int {
	if s.auto == nil || !s.auto() {
		return 0
	}
	return int(s.level.Load())
}

func newVideoSender(status *Status) *VideoSender {
	return &VideoSender{input: newLatest[[]byte](), status: status, session: sessionID()}
}

// Run владеет сокетом и seq; записи подписчиков обновляет отдельный reader.
// Клиент живёт 5 секунд после SUB, отправка ограничена deadline. UDP-подписка
// не является аутентификацией; не используйте этот протокол как публичный сервис.
func (s *VideoSender) Run(ctx context.Context, magic string, port int) {
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if e != nil {
		s.status.Set(e.Error())
		return
	}
	s.runOn(ctx, magic, conn)
}

func (s *VideoSender) runOn(ctx context.Context, magic string, conn *net.UDPConn) {
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
		name := localDiscoveryName()
		for {
			n, addr, e := conn.ReadFromUDPAddrPort(buf)
			if e != nil {
				return
			}
			if replyDiscovery(conn, net.UDPAddrFromAddrPort(addr), buf[:n], magic, name) {
				continue
			}
			addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
			prefix := magic + " SUB "
			if value, ok := strings.CutPrefix(string(buf[:n]), prefix); ok {
				p, e := strconv.Atoi(value)
				if e == nil && p > 0 && p < 65536 {
					key := netip.AddrPortFrom(addr.Addr(), uint16(p))
					now := time.Now().UnixNano()
					if current, ok := s.clients.Load(key); ok {
						current.(*videoPeer).lease.Store(now)
					} else {
						peer := &videoPeer{}
						peer.lease.Store(now)
						s.clients.Store(key, peer)
					}
				}
				continue
			}
			if peer, ok := s.clients.Load(addr); ok {
				if loss, ok := parseVideoFeedback(string(buf[:n]), magic, s.session); ok {
					p := peer.(*videoPeer)
					now := time.Now()
					p.lastFeedback.Store(now.UnixNano())
					p.report.Store(&peerReport{loss, now})
				}
			}
		}
	}()
	var seq uint32
	var bytes int
	stamp := time.Now()
	measuredAt := stamp
	var adaptation videoAdaptation
	var attempted, failed int
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case jpg := <-s.input.ch:
			now := time.Now()
			if now.Sub(measuredAt) >= adaptiveInterval {
				subscribers, loss := 0, -1
				s.clients.Range(func(_, value any) bool {
					peer := value.(*videoPeer)
					if now.Sub(time.Unix(0, peer.lease.Load())) <= 5*time.Second {
						subscribers++
						if report := peer.report.Swap(nil); report != nil && now.Sub(report.at) <= 3*time.Second {
							loss = max(loss, report.loss)
						} else if last := peer.lastFeedback.Load(); attempted >= 6 && last != 0 && now.Sub(time.Unix(0, last)) > 9*time.Second {
							// SUBs still arrive. A formerly reporting receiver can
							// lose the downlink entirely and have no JPEG to report.
							loss = max(loss, 12)
						}
					}
					return true
				})
				if attempted >= 6 && failed > 0 {
					loss = max(loss, failed*100/attempted)
				}
				if s.auto != nil && s.auto() {
					adaptation.observe(subscribers, loss)
				} else {
					adaptation = videoAdaptation{}
				}
				s.level.Store(int32(adaptation.level))
				measuredAt, attempted, failed = now, 0, 0
			}
			level := s.currentLevel()
			seq++
			count := 0
			s.clients.Range(func(key, value any) bool {
				if now.Sub(time.Unix(0, value.(*videoPeer).lease.Load())) > 5*time.Second {
					s.clients.Delete(key)
					return true
				}
				count++
				attempted++
				to := key.(netip.AddrPort)
				// One deadline per frame and client bounds stale work on a slow socket.
				_ = conn.SetWriteDeadline(time.Now().Add(45 * time.Millisecond))
				err := fragments(magic, s.session, seq, jpg, func(p []byte) error {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					n, e := conn.WriteToUDPAddrPort(p, to)
					bytes += n
					return e
				})
				if err != nil {
					failed++
				}
				return true
			})
			if count != 0 {
				// The same socket also answers FIND while video has no subscribers.
				_ = conn.SetWriteDeadline(time.Time{})
			}
			if now.Sub(stamp) >= time.Second {
				text := fmt.Sprintf("%d клиентов | %.1f Мбит/с", count, float64(bytes)*8/now.Sub(stamp).Seconds()/1e6)
				if level > 0 {
					text += fmt.Sprintf(" | Авто: %d%% FPS", adaptiveFPS[level])
				}
				s.status.Set(text)
				stamp = now
				bytes = 0
			}
		}
	}
}
