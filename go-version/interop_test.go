package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These optional tests exercise the actual 4.3 Python implementation. Building
// and running the Go app never requires Python or its modules.
func legacyPython(t *testing.T, modules string) string {
	t.Helper()
	python, e := exec.LookPath("python")
	if e != nil {
		t.Skip("optional legacy interoperability: Python unavailable")
	}
	cmd := exec.Command(python, "-c", "import "+modules)
	cmd.Dir = ".."
	if e := cmd.Run(); e != nil {
		t.Skip("optional legacy interoperability: Python modules unavailable")
	}
	return python
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	p := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return p
}

func TestGoVideoToLegacyPython(t *testing.T) {
	python := legacyPython(t, "network, cv2, numpy")
	port := freeUDPPort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	sender := newVideoSender(&Status{})
	go sender.Run(ctx, "WS40", port)
	var jpg bytes.Buffer
	_ = jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 64, 48)), nil)
	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sender.input.Put(jpg.Bytes())
			}
		}
	}()
	code := `import sys,time,threading,network
stop=threading.Event(); got=[]
r=network.Receiver(stop,lambda:{'host':'127.0.0.1','receive_fps':0,'decode_scale':1},lambda f:got.append(f) if f is not None else None,port=int(sys.argv[1]),magic=b'WS40')
thread=threading.Thread(target=r.run,daemon=True);thread.start()
until=time.monotonic()+5
while not got and time.monotonic()<until: time.sleep(.01)
stop.set();thread.join(.5)
assert got and got[0].shape[:2]==(48,64),r.status
print('OK')`
	cmd := exec.CommandContext(ctx, python, "-c", code, strconv.Itoa(port))
	cmd.Dir = ".."
	output, e := cmd.CombinedOutput()
	if e != nil || !strings.Contains(string(output), "OK") {
		t.Fatalf("legacy receiver: %v %s", e, output)
	}
}

func TestLegacyPythonVideoToGo(t *testing.T) {
	python := legacyPython(t, "network, cv2, numpy")
	port := freeUDPPort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	code := `import sys,time,threading,network,numpy as np
stop=threading.Event();frame=np.full((48,64,3),75,dtype=np.uint8)
s=network.Sender(stop,lambda:frame,lambda:{'quality':80},port=int(sys.argv[1]),magic=b'WC30')
threading.Timer(6,stop.set).start();s.run()`
	cmd := exec.CommandContext(ctx, python, "-c", code, strconv.Itoa(port))
	cmd.Dir = ".."
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	input := newLatest[[]byte]()
	go receiveVideo(ctx, "WC30", port, "127.0.0.1", input, &Status{})
	select {
	case jpg := <-input.ch:
		img, e := jpeg.Decode(bytes.NewReader(jpg))
		if e != nil || img.Bounds().Dx() != 64 || img.Bounds().Dy() != 48 {
			t.Fatal("legacy JPEG", e)
		}
	case <-ctx.Done():
		t.Fatal("legacy sender timed out")
	}
}

func TestLegacyPythonAudioCompatibility(t *testing.T) {
	python := legacyPython(t, "audio_stream, numpy")
	seq := uint32(12)
	var packet []byte
	pcmPackets([]float32{-.5, .5, 0, 1}, 48000, 2, 99, &seq, func(p []byte) { packet = p })
	code := `import sys,audio_stream as a,numpy as np
p=bytes.fromhex(sys.argv[1]);session,seq,rate,channels,data=a.unpack_audio(p)
assert (session,seq,rate,channels)==(99,12,48000,2)
assert np.allclose(data.reshape(-1),[-.5,.5,0,32767/32768])
print(next(a.audio_packets(data,rate,session,seq)).hex())`
	cmd := exec.Command(python, "-c", code, fmtHex(packet))
	cmd.Dir = ".."
	out, e := cmd.CombinedOutput()
	if e != nil || strings.TrimSpace(string(out)) != fmtHex(packet) {
		t.Fatalf("legacy PCM: %v %s", e, out)
	}
}

func fmtHex(p []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(p)*2)
	for i, b := range p {
		out[2*i] = digits[b>>4]
		out[2*i+1] = digits[b&15]
	}
	return string(out)
}
