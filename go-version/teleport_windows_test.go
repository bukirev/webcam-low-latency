package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net"
	"testing"
	"time"
)

func TestOBSBridgeUDPToTeleport(t *testing.T) {
	var inputs [3]*net.UDPConn
	for i := range inputs {
		var err error
		inputs[i], err = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { inputs[i].Close() })
	}
	port := func(i int) int { return inputs[i].LocalAddr().(*net.UDPAddr).Port }
	b, err := startOBSBridge(obsConfig{"127.0.0.1", port(1), port(2)}, port(0), [3]int{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	var jpg bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	for i, f := range b.feeds {
		address := fmt.Sprintf("127.0.0.1:%d", f.localPort)
		for round := 0; round < 2; round++ {
			c, err := net.DialTimeout("tcp4", address, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			deadline := time.Now().Add(4 * time.Second)
			_ = c.SetReadDeadline(deadline)
			_ = inputs[i].SetReadDeadline(deadline)
			var ping [128]byte
			n, peer, err := inputs[i].ReadFromUDP(ping[:])
			if err != nil {
				t.Fatal(err)
			}
			if string(ping[:n]) != fmt.Sprintf("%s SUB %d", f.magic, peer.Port) {
				t.Fatal("invalid subscription")
			}
			send := func(p []byte) error { _, err := inputs[i].WriteToUDP(p, peer); return err }
			kind, metadata, want := "JPEG", 88, jpg.Bytes()
			if i == 2 {
				kind, metadata, want = "WAVE", 16, []byte{0, 0xc0, 0, 0x40}
				seq := uint32(round)
				pcmPackets([]float32{-.5, .5}, 48000, 2, 42, &seq, func(p []byte) {
					if err := send(p); err != nil {
						t.Fatal(err)
					}
				})
			} else {
				jpegSeq := uint32(round * 2)
				if err := fragments(f.magic, 42, jpegSeq, jpg.Bytes()[:jpg.Len()-2], send); err != nil {
					t.Fatal(err)
				}
				// Give the receiver time to process the incomplete frame before
				// publishing the valid frame through the latest-value mailbox.
				until := time.Now().Add(30 * time.Millisecond)
				_ = c.SetReadDeadline(until)
				var unexpected [1]byte
				if _, err := c.Read(unexpected[:]); err == nil {
					t.Fatal("truncated JPEG was sent to OBS")
				}
				_ = c.SetReadDeadline(deadline)
				padded := append(bytes.Clone(jpg.Bytes()), 0, 0, 0, 0)
				if err := fragments(f.magic, 42, jpegSeq+1, padded, send); err != nil {
					t.Fatal(err)
				}
			}
			header := make([]byte, 16+metadata)
			if _, err := io.ReadFull(c, header); err != nil {
				t.Fatal(err)
			}
			if string(header[:4]) != kind || int(binary.LittleEndian.Uint32(header[12:])) != len(want) {
				t.Fatalf("unexpected header %x", header)
			}
			if i == 2 && (binary.LittleEndian.Uint32(header[16:]) != 2 ||
				binary.LittleEndian.Uint32(header[20:]) != 48000 ||
				binary.LittleEndian.Uint32(header[24:]) != 2 ||
				binary.LittleEndian.Uint32(header[28:]) != 1) {
				t.Fatal("unexpected PCM format")
			}
			got := make([]byte, len(want))
			if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("payload is not bit-exact: %v", err)
			}
			c.Close()
		}
	}
}
