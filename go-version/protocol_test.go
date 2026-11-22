package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"math/rand"
	"net"
	"testing"
	"time"
)

func TestFragmentCompatibilityAndReordering(t *testing.T) {
	data := make([]byte, 200000)
	rand.New(rand.NewSource(42)).Read(data)
	var packets [][]byte
	e := fragments("WC30", 123, 0xffffffff, data, func(p []byte) error { packets = append(packets, append([]byte(nil), p...)); return nil })
	if e != nil {
		t.Fatal(e)
	}
	if binary.BigEndian.Uint64(packets[0][4:12]) != 123 || len(packets[0]) != 1320 {
		t.Fatal("Python header mismatch")
	}
	rand.New(rand.NewSource(4)).Shuffle(len(packets), func(i, j int) { packets[i], packets[j] = packets[j], packets[i] })
	a := newAssembler("WC30")
	var output []byte
	for _, p := range packets {
		if b, _ := a.Push(p, time.Now()); b != nil {
			output = b
		}
	}
	if !bytes.Equal(data, output) {
		t.Fatal("reassembly mismatch")
	}
	if b, _ := a.Push(packets[0], time.Now()); b != nil {
		t.Fatal("duplicate accepted")
	}
	var next []byte
	_ = fragments("WC30", 123, 0, []byte("next"), func(p []byte) error { next = append([]byte(nil), p...); return nil })
	if b, _ := a.Push(next, time.Now()); string(b) != "next" {
		t.Fatal("sequence wrap rejected")
	}
}
func TestAssemblerBoundsAndExpiry(t *testing.T) {
	a := newAssembler("WS40")
	var packets [][]byte
	_ = fragments("WS40", 1, 1, make([]byte, 1500), func(p []byte) error { packets = append(packets, append([]byte(nil), p...)); return nil })
	a.Push(packets[0], time.Now().Add(-time.Second))
	if b, _ := a.Push(packets[1], time.Now()); b != nil {
		t.Fatal("expired frame completed")
	}
	for _, p := range [][]byte{{}, packets[0][:10], append([]byte("FAIL"), packets[0][4:]...)} {
		if b, _ := a.Push(p, time.Now()); b != nil {
			t.Fatal("invalid packet accepted")
		}
	}
}
func TestPCMCompatibilityAndClipping(t *testing.T) {
	samples := []float32{-2, -1, 0, .25, .9, 2}
	seq := uint32(0xffffffff)
	var packets [][]byte
	pcmPackets(samples, 48000, 2, 99, &seq, func(p []byte) { packets = append(packets, p) })
	a, e := parseAudio(packets[0])
	if e != nil {
		t.Fatal(e)
	}
	if len(packets[0]) != 23+len(samples)*2 || a.Session != 99 || a.Seq != 0xffffffff || seq != 0 {
		t.Fatal("Python audio header mismatch")
	}
	for i, want := range []float32{-1, -1, 0, .25, .9, 32767.0 / 32768} {
		if math.Abs(float64(a.Samples[i]-want)) > 1.0/32768 {
			t.Fatal(i, a.Samples)
		}
	}
	if _, e = parseAudio(packets[0][:len(packets[0])-1]); e == nil {
		t.Fatal("truncated PCM accepted")
	}
}
func TestRingBoundedAndSilentUnderrun(t *testing.T) {
	r := newRing(4)
	r.Push([]float32{1, 2, 3, 4, 5, 6})
	got, n := r.Pop(6, 0, 0)
	if n != 4 || !equalFloat(got, []float32{3, 4, 5, 6, 0, 0}) {
		t.Fatal(got, n)
	}
	r.Push([]float32{1, 2})
	if _, n = r.Pop(2, 4, 0); n != 0 || r.Len() != 2 {
		t.Fatal("prebuffer consumed")
	}
}
func equalFloat(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > 1e-4 {
			return false
		}
	}
	return true
}
func TestStreamingResamplerKeepsDuration(t *testing.T) {
	for _, pair := range [][2]int{{44100, 48000}, {48000, 16000}, {48000, 44100}} {
		data := make([]float32, pair[0])
		for i := range data {
			data[i] = float32(math.Sin(float64(i) * 440 * 2 * math.Pi / float64(pair[0])))
		}
		whole := newResampler(pair[0], pair[1], 2).Process(data, 1)
		stream := newResampler(pair[0], pair[1], 2)
		var split []float32
		for i := 0; i < len(data); i += 173 {
			split = append(split, stream.Process(data[i:min(i+173, len(data))], 1)...)
		}
		if !equalFloat(whole, split) || math.Abs(float64(len(split)/2-pair[1])) > 2 {
			t.Fatal(pair, len(split), len(whole))
		}
	}
}
func TestLatestMailboxNeverQueuesOldFrames(t *testing.T) {
	l := newLatest[int]()
	for i := 0; i < 1000; i++ {
		l.Put(i)
	}
	if <-l.ch != 999 || len(l.ch) != 0 {
		t.Fatal("unbounded/stale mailbox")
	}
}
func TestUDPVideoLoopback(t *testing.T) {
	probe, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender := newVideoSender(&Status{})
	go sender.Run(ctx, "WS40", port)
	received := newLatest[[]byte]()
	go receiveVideo(ctx, "WS40", port, "127.0.0.1", received, &Status{})
	data := bytes.Repeat([]byte{42}, 20000)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(4 * time.Second)
	for {
		select {
		case <-ticker.C:
			sender.input.Put(data)
		case got := <-received.ch:
			if !bytes.Equal(got, data) {
				t.Fatal("wire mismatch")
			}
			return
		case <-timeout:
			t.Fatal("UDP timed out")
		}
	}
}
func TestClientIgnoresUnusedSenderSettings(t *testing.T) {
	c := defaultConfig("client")
	c.Camera["fps"] = "unused"
	c.Screen["sample_rate"] = "unused"
	if e := validate(c); e != nil {
		t.Fatal(e)
	}
	c.Screen["video_port"] = "5001"
	if validate(c) == nil {
		t.Fatal("port conflict accepted")
	}
}
