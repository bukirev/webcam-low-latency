package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
)

// AudioPacket содержит межканальные float32-сэмплы после разбора PCM16.
// Frames — число временных отсчётов, len(Samples) == Frames*Channels.
type AudioPacket struct {
	Session                uint64
	Seq                    uint32
	Rate, Channels, Frames int
	Samples                []float32
}

// parseAudio проверяет полный размер и допустимый формат до чтения PCM.
// Заголовок big-endian, сэмплы little-endian: единый endian для всего пакета неверен.
func parseAudio(p []byte) (AudioPacket, error) {
	a := AudioPacket{}
	if len(p) <= 23 || len(p) > 1223 || string(p[:4]) != "WA40" {
		return a, fmt.Errorf("неверный аудиопакет")
	}
	a.Session = binary.BigEndian.Uint64(p[4:12])
	a.Seq = binary.BigEndian.Uint32(p[12:16])
	a.Rate = int(binary.BigEndian.Uint32(p[16:20]))
	a.Frames = int(binary.BigEndian.Uint16(p[20:22]))
	a.Channels = int(p[22])
	if (a.Rate != 16000 && a.Rate != 24000 && a.Rate != 44100 && a.Rate != 48000) || (a.Channels != 1 && a.Channels != 2) || a.Frames < 1 || len(p) != 23+a.Frames*a.Channels*2 {
		return a, fmt.Errorf("неверный формат PCM")
	}
	a.Samples = make([]float32, a.Frames*a.Channels)
	for i := range a.Samples {
		a.Samples[i] = float32(int16(binary.LittleEndian.Uint16(p[23+i*2:]))) / 32768
	}
	return a, nil
}

// pcmPackets ограничивает payload 1200 байт и режет только по границам frames.
// Клиппинг до 32767/32768 предотвращает переполнение положительного int16.
func pcmPackets(samples []float32, rate, channels int, session uint64, seq *uint32, send func([]byte)) {
	maxSamples := 1200 / 2
	maxSamples -= maxSamples % channels
	for len(samples) > 0 {
		n := min(maxSamples, len(samples))
		p := make([]byte, 23+n*2)
		copy(p, "WA40")
		binary.BigEndian.PutUint64(p[4:12], session)
		binary.BigEndian.PutUint32(p[12:16], *seq)
		*seq++
		binary.BigEndian.PutUint32(p[16:20], uint32(rate))
		binary.BigEndian.PutUint16(p[20:22], uint16(n/channels))
		p[22] = byte(channels)
		for i, x := range samples[:n] {
			x = max(-1, min(float32(32767.0/32768), x))
			binary.LittleEndian.PutUint16(p[23+i*2:], uint16(int16(x*32768)))
		}
		send(p)
		samples = samples[n:]
	}
}

// FloatRing хранит отдельные float32-сэмплы, не кадры и не миллисекунды.
// При переполнении удаляются старейшие сэмплы; capacity рассчитывают с channels.
// Методы защищены mutex, но пакетное выравнивание задаётся вызывающим кодом.
type FloatRing struct {
	mu         sync.Mutex
	data       []float32
	read, size int
}

func newRing(capacity int) *FloatRing { return &FloatRing{data: make([]float32, max(1, capacity))} }
func (r *FloatRing) Push(samples []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range samples {
		if r.size == len(r.data) {
			r.read = (r.read + 1) % len(r.data)
			r.size--
		}
		r.data[(r.read+r.size)%len(r.data)] = s
		r.size++
	}
}
func (r *FloatRing) Len() int { r.mu.Lock(); defer r.mu.Unlock(); return r.size }

// Pop возвращает ровно n сэмплов, дополняя нехватку нулями; count — реальные данные.
// minimum задаёт предварительное накопление, keep отбрасывает чрезмерное отставание.
func (r *FloatRing) Pop(n, minimum, keep int) ([]float32, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]float32, n)
	if keep > 0 && r.size > keep {
		skip := r.size - keep
		r.read = (r.read + skip) % len(r.data)
		r.size = keep
	}
	if r.size < minimum {
		return out, 0
	}
	count := min(n, r.size)
	for i := 0; i < count; i++ {
		out[i] = r.data[r.read]
		r.read = (r.read + 1) % len(r.data)
	}
	r.size -= count
	return out, count
}

// Resampler сохраняет дробную позицию и граничные сэмплы между блоками.
// Он принадлежит одному потоку обработки; это линейная интерполяция, без
// полноценного антиалиас-фильтра и адаптации к дрейфу независимых часов устройств.
type Resampler struct {
	from, to, channels int
	position           float64
	pending            []float32
}

func newResampler(from, to, channels int) *Resampler {
	return &Resampler{from: from, to: to, channels: channels}
}

// Process сначала меняет число каналов, затем частоту. Последний исходный frame
// может остаться pending, чтобы следующий блок продолжил интерполяцию без разрыва.
func (r *Resampler) Process(input []float32, sourceChannels int) []float32 {
	frames := len(input) / sourceChannels
	converted := make([]float32, frames*r.channels)
	for i := 0; i < frames; i++ {
		for ch := 0; ch < r.channels; ch++ {
			var v float32
			if r.channels == 1 {
				for c := 0; c < sourceChannels; c++ {
					v += input[i*sourceChannels+c]
				}
				v /= float32(sourceChannels)
			} else {
				v = input[i*sourceChannels+min(ch, sourceChannels-1)]
			}
			converted[i*r.channels+ch] = v
		}
	}
	if r.from == r.to {
		return converted
	}
	r.pending = append(r.pending, converted...)
	n := len(r.pending) / r.channels
	out := make([]float32, 0, int(float64(n)*float64(r.to)/float64(r.from)+2)*r.channels)
	step := float64(r.from) / float64(r.to)
	for r.position < float64(n-1) {
		i := int(r.position)
		f := float32(r.position - float64(i))
		for ch := 0; ch < r.channels; ch++ {
			a, b := r.pending[i*r.channels+ch], r.pending[(i+1)*r.channels+ch]
			out = append(out, a+(b-a)*f)
		}
		r.position += step
	}
	drop := min(int(r.position), n)
	copy(r.pending, r.pending[drop*r.channels:])
	r.pending = r.pending[:len(r.pending)-drop*r.channels]
	r.position -= float64(drop)
	return out
}

// decodeNative читает устройство в его нативном формате и нормализует амплитуду.
// Нельзя считать, что любой endpoint WASAPI выдаёт PCM16/стерео/48000 Гц.
func decodeNative(data []byte, bits, channels int, floating bool) []float32 {
	count := len(data) / (bits / 8)
	out := make([]float32, count)
	for i := range out {
		p := data[i*(bits/8):]
		switch {
		case floating && bits == 32:
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(p))
		case bits == 16:
			out[i] = float32(int16(binary.LittleEndian.Uint16(p))) / 32768
		case bits == 24:
			v := int32(p[0]) | int32(p[1])<<8 | int32(int8(p[2]))<<16
			out[i] = float32(v) / 8388608
		case bits == 32:
			out[i] = float32(int32(binary.LittleEndian.Uint32(p))) / 2147483648
		case bits == 8:
			out[i] = (float32(p[0]) - 128) / 128
		}
	}
	return out
}

// encodeNative пишет сэмплы в формат endpoint; out должен вместить весь блок.
// Владельцем нативного render buffer остаётся WASAPI до ReleaseBuffer.
func encodeNative(out []byte, samples []float32, bits int, floating bool) {
	for i, s := range samples {
		s = max(-1, min(float32(.999999), s))
		p := out[i*(bits/8):]
		switch {
		case floating && bits == 32:
			binary.LittleEndian.PutUint32(p, math.Float32bits(s))
		case bits == 16:
			binary.LittleEndian.PutUint16(p, uint16(int16(s*32767)))
		case bits == 24:
			v := int32(s * 8388607)
			p[0] = byte(v)
			p[1] = byte(v >> 8)
			p[2] = byte(v >> 16)
		case bits == 32:
			binary.LittleEndian.PutUint32(p, uint32(int32(float64(s)*2147483647)))
		case bits == 8:
			p[0] = byte(s*127 + 128)
		}
	}
}
