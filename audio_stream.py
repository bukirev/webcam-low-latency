# Python WASAPI через PyAudioWPatch: отдельные источники, bounded PCM buffer и WA40.
# Frames и отдельные сэмплы различаются числом channels. Не применять mailbox видео
# к последовательному PCM. Подробнее: docs/modules/audio-core.md, docs/legacy-python.md.
"""Small, bounded PCM packets over LAN. Capture and playback use WASAPI.

There is deliberately no reliable delivery or growing playback queue: late audio
is discarded, and missing packets become silence. The camera protocol is separate.
"""
from collections import deque
from contextlib import contextmanager
from functools import wraps
import secrets
import socket
import struct
import threading
import time

import numpy as np
import pyaudiowpatch as pa

AUDIO_PORT = 5003
AUDIO_MAGIC = b'WA40'
AUDIO_HEADER = struct.Struct('!4sQIIHB')
SAMPLE_RATES = (16000, 24000, 44100, 48000)
MAX_PAYLOAD = 1200
_PA_LOCK = threading.RLock()


# Lifecycle PortAudio сериализован отдельно от PCM callbacks. Держать этот
# lock в real-time callback или ждать GUI из него нельзя.
def serialized_portaudio(function):
    @wraps(function)
    def call(*args, **kwargs):
        with _PA_LOCK:
            return function(*args, **kwargs)
    return call


@contextmanager
def audio_context():
    # PortAudio initialization, enumeration, stream creation and teardown are not
    # thread-safe. Callbacks only access PCMBuffer and never acquire this lock.
    with _PA_LOCK:
        manager = pa.PyAudio()
    try:
        yield manager
    finally:
        with _PA_LOCK:
            manager.terminate()


def newer(seq, previous):
    return previous is None or 0 < ((seq - previous) & 0xffffffff) < 0x80000000


def block_sizes(rate, block_ms):
    remainder = 0
    while True:
        remainder += rate * block_ms
        count, remainder = divmod(remainder, 1000)
        yield count


def unpack_audio(packet):
    if not AUDIO_HEADER.size < len(packet) <= AUDIO_HEADER.size + MAX_PAYLOAD:
        return None
    magic, session, seq, rate, frames, channels = AUDIO_HEADER.unpack_from(packet)
    if magic != AUDIO_MAGIC or rate not in SAMPLE_RATES or channels not in (1, 2):
        return None
    if frames < 1 or len(packet) != AUDIO_HEADER.size + frames * channels * 2:
        return None
    pcm = np.frombuffer(packet, dtype='<i2', offset=AUDIO_HEADER.size)
    return session, seq, rate, channels, pcm.reshape(-1, channels).astype(np.float32) / 32768


def audio_packets(samples, rate, session, seq):
    channels = samples.shape[1]
    size = MAX_PAYLOAD // (channels * 2)
    pcm = (np.clip(samples, -1, 32767 / 32768) * 32768).astype('<i2')
    for offset in range(0, len(pcm), size):
        chunk = pcm[offset:offset + size]
        yield AUDIO_HEADER.pack(AUDIO_MAGIC, session, seq, rate, len(chunk), channels) + chunk.tobytes()
        seq = (seq + 1) & 0xffffffff


def change_channels(data, channels):
    if data.shape[1] == channels:
        return data
    if channels == 1:
        return data.mean(axis=1, keepdims=True)
    if data.shape[1] == 1:
        return np.repeat(data, 2, axis=1)
    return data[:, :2]


# Дробная фаза и последний frame сохраняются между блоками; пересоздание
# resampler на каждом block искажает суммарную длительность.
class Resampler:
    """Continuous linear resampling without rounding a duration per callback."""
    def __init__(self, source_rate, target_rate, channels):
        self.step = source_rate / target_rate
        self.channels = channels
        self.tail = np.empty((0, channels), np.float32)
        self.position = 0.

    def process(self, data):
        data = change_channels(data, self.channels)
        if self.step == 1:
            return data
        data = np.concatenate((self.tail, data))
        positions = np.arange(self.position, max(0, len(data) - 1), self.step)
        if not len(positions):
            self.tail = data
            return np.empty((0, self.channels), np.float32)
        left = positions.astype(np.intp)
        alpha = (positions - left).astype(np.float32)[:, None]
        result = data[left] * (1 - alpha) + data[left + 1] * alpha
        next_position = positions[-1] + self.step
        drop = min(int(next_position), len(data))
        self.tail = data[drop:].copy()
        self.position = next_position - drop
        return result


# Capacity и pull выражены frames, в отличие от Go FloatRing с отдельными samples.
# При переносе умножение на channels должно происходить ровно один раз.
class PCMBuffer:
    """A bounded FIFO shared by the audio and network callbacks."""
    def __init__(self, channels, max_frames):
        self.channels = channels
        self.max_frames = max_frames
        self.parts = deque()
        self.frames = 0
        self.lock = threading.Lock()
        self.dropped = 0

    def _discard(self, n):
        while n and self.parts:
            part = self.parts[0]
            take = min(n, len(part))
            if take == len(part):
                self.parts.popleft()
            else:
                self.parts[0] = part[take:]
            self.frames -= take
            n -= take

    def push(self, data):
        if not len(data):
            return
        with self.lock:
            self.parts.append(data.copy())
            self.frames += len(data)
            extra = max(0, self.frames - self.max_frames)
            self.dropped += extra
            self._discard(extra)

    def pull(self, n, *, minimum=0, keep=None):
        result = np.zeros((n, self.channels), np.float32)
        with self.lock:
            if keep is not None and self.frames > keep:
                self.dropped += self.frames - keep
                self._discard(self.frames - keep)
            if self.frames < minimum:
                return result, 0
            offset = 0
            while offset < n and self.parts:
                part = self.parts[0]
                take = min(n - offset, len(part))
                result[offset:offset + take] = part[:take]
                self._discard(take)
                offset += take
        return result, offset


@serialized_portaudio
def list_devices():
    """Only list real WASAPI endpoints, avoiding duplicate MME/DirectSound names."""
    result = {'system_device': [], 'mic_device': [], 'output_device': []}
    with audio_context() as p:
        host = p.get_host_api_info_by_type(pa.paWASAPI)['index']
        for device in p.get_device_info_generator():
            if device['hostApi'] != host:
                continue
            key = str(device['index']) + ': ' + device['name']
            if device.get('isLoopbackDevice'):
                result['system_device'].append(key)
            elif device['maxInputChannels']:
                result['mic_device'].append(key)
            if device['maxOutputChannels'] and not device.get('isLoopbackDevice'):
                result['output_device'].append(key)
    return result


@serialized_portaudio
def select_device(p, choice, kind):
    if choice != 'По умолчанию':
        # Device names are persisted; indices can change after a USB reconnect.
        name = choice.split(': ', 1)[-1]
        for d in p.get_device_info_generator():
            wasapi = p.get_host_api_info_by_type(pa.paWASAPI)['index']
            if d['hostApi'] != wasapi or d['name'] != name:
                continue
            if kind == 'system' and d.get('isLoopbackDevice'):
                return d
            if kind == 'mic' and d['maxInputChannels'] and not d.get('isLoopbackDevice'):
                return d
            if kind == 'output' and d['maxOutputChannels'] and not d.get('isLoopbackDevice'):
                return d
        raise ValueError('Устройство отключено: ' + name)
    if kind == 'system':
        return p.get_default_wasapi_loopback()
    return p.get_default_wasapi_device(d_out=(kind == 'output'))


class CaptureSource:
    @serialized_portaudio
    def __init__(self, p, choice, kind, rate, channels, block_ms):
        device = select_device(p, choice, kind)
        native_rate = int(device['defaultSampleRate'])
        native_channels = min(int(device['maxInputChannels']), max(channels, 2 if kind == 'system' else 1))
        if native_channels < 1:
            raise ValueError('Устройство не поддерживает захват звука')
        self.buffer = PCMBuffer(channels, rate // 10)
        self.resampler = Resampler(native_rate, rate, channels)
        self.name = device['name']

        def callback(data, frame_count, time_info, flags):
            samples = np.frombuffer(data, np.float32).reshape(-1, native_channels)
            self.buffer.push(self.resampler.process(samples))
            return None, pa.paContinue

        self.stream = p.open(format=pa.paFloat32, channels=native_channels,
                             rate=native_rate, input=True, input_device_index=device['index'],
                             frames_per_buffer=max(64, int(native_rate * block_ms / 1000)),
                             stream_callback=callback)

    @serialized_portaudio
    def close(self):
        self.stream.close()


# Системный звук и микрофон независимы по устройствам/часам. Перед mix оба
# источника приводятся к общим rate/channels; отсутствие данных означает тишину.
class AudioSender:
    def __init__(self, stop, get_config):
        self.stop = stop
        self.get_config = get_config
        self.status = 'Звук: запуск…'
        self.peak = 0.
        self.clients = {}
        self.session = secrets.randbits(64)
        self.seq = 0

    def run(self):
        streams = []
        try:
            c = self.get_config()
            rate, channels = int(c['sample_rate']), int(c['channels'])
            block_ms = int(c['block_ms'])
            errors = []
            with audio_context() as p, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                local_only = c.get('role') == 'local'
                sock.bind(('127.0.0.1', 0) if local_only else ('', int(c['audio_port'])))
                sock.setblocking(False)
                for kind in ('system', 'mic'):
                    if c[kind + '_enabled']:
                        try:
                            source = CaptureSource(p, c[kind + '_device'], kind, rate, channels, block_ms)
                            streams.append((kind, source))
                        except (OSError, ValueError, LookupError) as e:
                            errors.append(('Система' if kind == 'system' else 'Микрофон') + ': ' + str(e))
                if not streams:
                    raise ValueError('; '.join(errors) or 'Не выбран источник звука')
                try:
                    next_block = time.monotonic()
                    stamp = next_block
                    total = 0
                    sizes = block_sizes(rate, block_ms)
                    while not self.stop.is_set():
                        now = time.monotonic()
                        for _ in range(32):
                            try:
                                message, addr = sock.recvfrom(256)
                            except BlockingIOError:
                                break
                            if not local_only and message == AUDIO_MAGIC + f' SUB {addr[1]}'.encode():
                                self.clients[addr] = now
                        self.clients = {addr: t for addr, t in self.clients.items() if now - t < 5}
                        if now >= next_block:
                            c = self.get_config()
                            frames = next(sizes)
                            mixed = np.zeros((frames, channels), np.float32)
                            for kind, source in streams:
                                samples, _ = source.buffer.pull(frames)
                                mixed += samples * float(c[kind + '_gain']) / 100
                            if c['send_mute']:
                                mixed.fill(0)
                            self.peak = min(1., float(np.max(np.abs(mixed))))
                            np.clip(mixed, -1, 1, out=mixed)
                            for packet in audio_packets(mixed, rate, self.session, self.seq):
                                self.seq = (self.seq + 1) & 0xffffffff
                                for addr in list(self.clients):
                                    try:
                                        total += sock.sendto(packet, addr)
                                    except OSError:
                                        pass
                            next_block = max(next_block + block_ms / 1000, now)
                        if now - stamp >= 1:
                            rate_mbit = total * 8 / (now - stamp) / 1e6
                            self.status = f'Звук: {len(self.clients)} клиентов | {rate} Гц / {channels} кан. | {rate_mbit:.2f} Мбит/с'
                            if local_only:
                                self.status = f'Звук: локальный уровень | {rate} Гц / {channels} кан. | без передачи'
                            if errors:
                                self.status += ' | ' + '; '.join(errors)
                            total = 0
                            stamp = now
                        self.stop.wait(.001)
                finally:
                    for _, source in streams:
                        source.close()
        except Exception as e:
            self.status = 'Ошибка звука: ' + str(e)


class AudioPlayback:
    @serialized_portaudio
    def __init__(self, p, config, rate, channels):
        self.get_config = config
        c = config()
        device = select_device(p, c['output_device'], 'output')
        self.rate = int(device['defaultSampleRate'])
        self.channels = min(channels, int(device['maxOutputChannels']))
        self.resampler = Resampler(rate, self.rate, self.channels)
        self.buffer_ms = int(c['buffer_ms'])
        self.target = max(1, int(self.rate * self.buffer_ms / 1000))
        self.buffer = PCMBuffer(self.channels, self.target + self.rate // 10)
        self.waiting = True
        self.underruns = 0
        self.peak = 0.
        self.last_packet = time.monotonic()

        def callback(data, frames, time_info, flags):
            samples, count = self.buffer.pull(frames, minimum=self.target if self.waiting else 0,
                                              keep=self.target + self.rate // 50)
            if count:
                self.waiting = count < frames
                if count < frames:
                    self.underruns += 1
            else:
                self.waiting = True
            live = self.get_config()
            if live['receive_mute'] or time.monotonic() - self.last_packet > .4:
                samples.fill(0)
            else:
                samples *= float(live['volume']) / 100
            np.clip(samples, -1, 1, out=samples)
            self.peak = float(np.max(np.abs(samples))) if len(samples) else 0.
            return samples.astype(np.float32, copy=False).tobytes(), pa.paContinue

        self.stream = p.open(format=pa.paFloat32, channels=self.channels, rate=self.rate,
                             output=True, output_device_index=device['index'],
                             frames_per_buffer=max(64, int(self.rate * int(c['block_ms']) / 1000)),
                             stream_callback=callback)

    def push(self, samples):
        self.last_packet = time.monotonic()
        self.buffer.push(self.resampler.process(samples))

    @serialized_portaudio
    def close(self):
        self.stream.close()


class AudioReceiver:
    def __init__(self, stop, get_config):
        self.stop = stop
        self.get_config = get_config
        self.status = 'Звук: поиск передатчика…'
        self.peak = 0.
        self.source_ip = None

    def run(self):
        while not self.stop.is_set():
            try:
                self.receive()
            except Exception as e:
                self.status = 'Ошибка приёма звука: ' + str(e)
                self.stop.wait(1)

    def receive(self):
        player = None
        with _PA_LOCK:
            p = pa.PyAudio()
        try:
            c = self.get_config()
            host = c['host'].strip() or '255.255.255.255'
            expected_ip = socket.gethostbyname(host) if host != '255.255.255.255' else None
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 128 * 1024)
                sock.bind(('', 0))
                sock.settimeout(.01)
                port = sock.getsockname()[1]
                ping = last_seen = 0
                source = last_seq = audio_format = None
                stamp = time.monotonic()
                lost = received = 0
                while not self.stop.is_set():
                    now = time.monotonic()
                    if now - ping >= 1:
                        sock.sendto(AUDIO_MAGIC + f' SUB {port}'.encode(),
                                    (source[0] if source else host, int(c['audio_port'])))
                        ping = now
                    if source and now - last_seen > 2:
                        source = last_seq = None
                        self.source_ip = None
                        if player:
                            player.close()
                            player = None
                    try:
                        packet, addr = sock.recvfrom(1500)
                    except socket.timeout:
                        packet = None
                    parsed = unpack_audio(packet) if packet else None
                    if parsed:
                        session, seq, rate, channels, samples = parsed
                        if expected_ip and addr[0] != expected_ip:
                            continue
                        if source and (addr[0], session) != source:
                            continue
                        if not newer(seq, last_seq):
                            continue
                        if not source:
                            source = (addr[0], session)
                            self.source_ip = addr[0]
                        if player is None or audio_format != (rate, channels):
                            if player:
                                player.close()
                            player = AudioPlayback(p, self.get_config, rate, channels)
                            audio_format = rate, channels
                            last_seq = None
                        if last_seq is not None:
                            missing = ((seq - last_seq) & 0xffffffff) - 1
                            lost += missing
                            # A brief loss retains timing, a long outage starts fresh.
                            if 0 < missing <= 8:
                                player.push(np.zeros((missing * len(samples), channels), np.float32))
                        player.push(samples)
                        last_seq = seq
                        last_seen = now
                        received += len(packet)
                    self.peak = player.peak if player else 0.
                    if now - stamp >= 1:
                        self.status = (f'Звук: {source[0]} | {audio_format[0]} Гц / {audio_format[1]} кан. | '
                                       f'{received * 8 / (now - stamp) / 1e6:.2f} Мбит/с | потери: {lost} | '
                                       f'буфер {c["buffer_ms"]} мс | паузы: {player.underruns}' if source and player
                                       else 'Звук: ожидание передатчика…')
                        lost = received = 0
                        stamp = now
        finally:
            try:
                if player:
                    player.close()
            finally:
                with _PA_LOCK:
                    p.terminate()
