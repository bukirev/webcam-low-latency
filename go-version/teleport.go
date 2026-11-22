package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/color"
	"image/jpeg"
	"math"
	"net"
	"time"
)

// Teleport 0.8 writes a 16-byte header followed by format metadata and payload.
// Size excludes the metadata. Both timestamps and metadata are little-endian.
type teleportPacket struct {
	header  []byte
	payload []byte
	queued  time.Time
}

func teleportHeader(kind string, timestamp time.Duration, size, metadata int) []byte {
	h := make([]byte, 16+metadata)
	copy(h, kind)
	binary.LittleEndian.PutUint64(h[4:], uint64(timestamp))
	binary.LittleEndian.PutUint32(h[12:], uint32(size))
	return h
}

func teleportJPEG(jpg []byte, now time.Time, stamp time.Duration) (teleportPacket, error) {
	// Camera drivers may omit EOI or pad a sample past it. DecodeConfig alone
	// succeeds on a truncated frame because it reads only the headers.
	eoi := bytes.LastIndex(jpg, []byte{0xff, 0xd9})
	sos := bytes.Index(jpg, []byte{0xff, 0xda})
	if sos < 0 || eoi <= sos {
		return teleportPacket{}, errors.New("JPEG без конца кадра")
	}
	jpg = jpg[:eoi+2]
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
	if err != nil {
		return teleportPacket{}, errors.New("Повреждён заголовок JPEG")
	}
	// Teleport 0.8 computes subsampled plane strides using width/2 and height/2.
	if cfg.ColorModel != color.YCbCrModel || cfg.Width%2 != 0 || cfg.Height%2 != 0 ||
		cfg.Width < 2 || cfg.Height < 2 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return teleportPacket{}, errors.New("Нужен цветной JPEG с чётной шириной и высотой")
	}
	h := teleportHeader("JPEG", stamp, len(jpg), 88)
	// Full-range JPEG YCbCr, with neutral chroma at 128/255.
	const center = 128.0 / 255
	values := [...]float32{
		1, 0, 1.402, -1.402 * center,
		1, -.344136, -.714136, (.344136 + .714136) * center,
		1, 1.772, 0, -1.772 * center,
		0, 0, 0, 1,
		0, 0, 0, 1, 1, 1,
	}
	for i, v := range values {
		binary.LittleEndian.PutUint32(h[16+4*i:], math.Float32bits(v))
	}
	return teleportPacket{h, jpg, now}, nil
}

func teleportPCM(a AudioPacket, now time.Time, stamp time.Duration) teleportPacket {
	pcm := make([]byte, len(a.Samples)*2)
	for i, v := range a.Samples {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(v*32768)))
	}
	h := teleportHeader("WAVE", stamp, len(pcm), 16)
	binary.LittleEndian.PutUint32(h[16:], 2) // OBS AUDIO_FORMAT_16BIT
	binary.LittleEndian.PutUint32(h[20:], uint32(a.Rate))
	binary.LittleEndian.PutUint32(h[24:], uint32(a.Channels)) // OBS mono/stereo enum
	binary.LittleEndian.PutUint32(h[28:], uint32(a.Frames))
	return teleportPacket{h, pcm, now}
}

// One OBS connection per feed. A stalled writer is dropped so TCP cannot hold
// up UDP reception. Old queue entries are skipped after disconnect/reconnect.
func serveTeleport(ctx context.Context, listener net.Listener, packets <-chan teleportPacket, status *Status) {
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	for ctx.Err() == nil {
		status.Set("Ожидание OBS")
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				status.Set(err.Error())
			}
			return
		}
		status.Set("OBS подключён")
		sendTeleport(ctx, conn.(*net.TCPConn), packets)
	}
}

func sendTeleport(ctx context.Context, conn *net.TCPConn, packets <-chan teleportPacket) {
	defer conn.Close()
	_ = conn.SetNoDelay(true)
	// Teleport's manual input is receive-only. A read detects disconnects even
	// while the upstream source is silent.
	done := make(chan struct{})
	go func() {
		defer close(done)
		var b [1]byte
		_, _ = conn.Read(b[:])
		conn.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case p := <-packets:
			if time.Since(p.queued) > 150*time.Millisecond {
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(150 * time.Millisecond))
			wire := net.Buffers{p.header, p.payload}
			if _, err := wire.WriteTo(conn); err != nil {
				return
			}
		}
	}
}
