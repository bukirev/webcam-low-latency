import socket
import threading
import time
import unittest
from unittest.mock import patch

import numpy as np

import audio_stream as audio
from network import Receiver, Sender
from screen_capture import capture_region
from screen_window import DEFAULT, processed_preview, validate_config


def free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


class AudioTests(unittest.TestCase):
    def test_concurrent_audio_contexts_serialize_native_lifecycle(self):
        lock = threading.Lock()
        barrier = threading.Barrier(4)
        state = {'operations': 0, 'peak': 0, 'calls': 0}
        errors = []

        def native_operation():
            with lock:
                state['operations'] += 1
                state['peak'] = max(state['peak'], state['operations'])
                state['calls'] += 1
            time.sleep(.01)
            with lock:
                state['operations'] -= 1

        class Manager:
            def __init__(self): native_operation()
            def terminate(self): native_operation()

        def work():
            try:
                with audio.audio_context():
                    # Contexts can coexist; only native lifecycle calls serialize.
                    barrier.wait(timeout=2)
            except Exception as e:
                errors.append(e)

        with patch.object(audio.pa, 'PyAudio', Manager):
            threads = [threading.Thread(target=work) for _ in range(4)]
            for thread in threads: thread.start()
            for thread in threads: thread.join(3)
        self.assertFalse(errors)
        self.assertEqual(state['peak'], 1)
        self.assertEqual(state['calls'], 8)

    def test_fractional_blocks_do_not_drift_at_44100_hz(self):
        for rate in audio.SAMPLE_RATES:
            for block_ms in (5, 10, 20):
                sizes = audio.block_sizes(rate, block_ms)
                self.assertEqual(sum(next(sizes) for _ in range(1000 // block_ms)), rate)

    def test_pcm_packet_roundtrip_mtu_and_sequence_wrap(self):
        signal = np.linspace(-1.2, 1.2, 2000, dtype=np.float32).reshape(-1, 2)
        packets = list(audio.audio_packets(signal, 48000, 123, 0xfffffffe))
        self.assertGreater(len(packets), 2)
        decoded = []
        for i, packet in enumerate(packets):
            self.assertLessEqual(len(packet), 1232)
            session, seq, rate, channels, pcm = audio.unpack_audio(packet)
            self.assertEqual((session, seq, rate, channels), (123, (0xfffffffe + i) & 0xffffffff, 48000, 2))
            decoded.append(pcm)
        np.testing.assert_allclose(np.concatenate(decoded), np.clip(signal, -1, 32767 / 32768), atol=1 / 32768)
        self.assertTrue(audio.newer(0, 0xffffffff))
        self.assertFalse(audio.newer(0xffffffff, 0))
        self.assertFalse(audio.newer(100, 100))

    def test_invalid_packets_are_rejected(self):
        packet = next(audio.audio_packets(np.zeros((100, 2), np.float32), 48000, 1, 1))
        for bad in (b'', packet[:10], b'FAIL' + packet[4:], packet[:-1], packet + b'\0', b'x' * 65535):
            self.assertIsNone(audio.unpack_audio(bad))

    def test_fifo_discards_oldest_and_zero_fills_underrun(self):
        buffer = audio.PCMBuffer(1, 6)
        buffer.push(np.arange(5, dtype=np.float32)[:, None])
        buffer.push(np.arange(5, 10, dtype=np.float32)[:, None])
        result, count = buffer.pull(8)
        np.testing.assert_array_equal(result[:, 0], [4, 5, 6, 7, 8, 9, 0, 0])
        self.assertEqual(count, 6)
        self.assertEqual(buffer.dropped, 4)
        self.assertEqual(buffer.frames, 0)

    def test_fifo_prebuffer_preserves_waiting_samples(self):
        buffer = audio.PCMBuffer(1, 100)
        buffer.push(np.ones((8, 1), np.float32))
        self.assertEqual(buffer.pull(4, minimum=10)[1], 0)
        self.assertEqual(buffer.frames, 8)
        buffer.push(np.ones((4, 1), np.float32))
        self.assertEqual(buffer.pull(4, minimum=10)[1], 4)

    def test_resampling_chunks_preserves_time_and_waveform(self):
        for source_rate, target_rate in ((44100, 48000), (48000, 16000), (48000, 44100)):
            signal = np.sin(np.arange(source_rate, dtype=np.float32) * (2 * np.pi * 440 / source_rate))[:, None]
            whole = audio.Resampler(source_rate, target_rate, 2).process(signal)
            resampler = audio.Resampler(source_rate, target_rate, 2)
            pieces = [resampler.process(signal[i:i + 173]) for i in range(0, len(signal), 173)]
            split = np.concatenate(pieces)
            self.assertLessEqual(abs(len(split) - target_rate), 2)
            self.assertEqual(split.shape, whole.shape)
            np.testing.assert_allclose(split, whole, atol=2e-5)

    def test_audio_subscription_receives_pcm_and_closes_player_once(self):
        received = threading.Event()
        players = []
        tone = np.full((240, 2), .25, np.float32)

        class Player:
            peak = .25
            underruns = 0
            def __init__(self, *args):
                self.closed = 0
                players.append(self)
            def push(self, samples):
                np.testing.assert_allclose(samples, tone, atol=1 / 32768)
                received.set()
            def close(self):
                self.closed += 1

        stop = threading.Event()
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as server:
            server.bind(('127.0.0.1', 0))
            server.settimeout(3)
            config = dict(DEFAULT, role='client', host='127.0.0.1', audio_port=str(server.getsockname()[1]))
            receiver = audio.AudioReceiver(stop, lambda: config)
            with patch.object(audio, 'AudioPlayback', Player):
                thread = threading.Thread(target=receiver.run)
                thread.start()
                try:
                    subscription, addr = server.recvfrom(256)
                    self.assertEqual(subscription, audio.AUDIO_MAGIC + f' SUB {addr[1]}'.encode())
                    for packet in audio.audio_packets(tone, 48000, 99, 1):
                        server.sendto(packet, addr)
                    self.assertTrue(received.wait(2), receiver.status)
                finally:
                    stop.set()
                    thread.join(3)
                self.assertFalse(thread.is_alive())
        self.assertEqual(len(players), 1)
        self.assertEqual(players[0].closed, 1)


class VideoTests(unittest.TestCase):
    def test_camera_and_screen_protocols_work_concurrently(self):
        stop = threading.Event()
        threads = []
        events = []
        results = []
        try:
            for magic, value in ((b'WC30', 60), (b'WS40', 180)):
                frame = np.full((120, 160, 3), value, np.uint8)
                config = dict(host='127.0.0.1', quality=80)
                port = free_port()
                event = threading.Event()
                output = []
                def receive(f, target=output, ready=event):
                    if f is not None:
                        target.append(f)
                        ready.set()
                sender = Sender(stop, lambda f=frame: f, lambda c=config: c, port=port, magic=magic)
                receiver = Receiver(stop, lambda c=config: c, receive, port=port, magic=magic)
                for service in (sender, receiver):
                    thread = threading.Thread(target=service.run)
                    thread.start()
                    threads.append(thread)
                events.append(event)
                results.append((output, value))
            for event in events:
                self.assertTrue(event.wait(4), 'Video was not received')
            for frames, value in results:
                self.assertAlmostEqual(float(frames[0].mean()), value, delta=2)
        finally:
            stop.set()
            for thread in threads:
                thread.join(3)
                self.assertFalse(thread.is_alive())

    def test_preview_all_fit_modes_handle_extreme_aspect_ratios(self):
        for size in ((1, 100), (100, 1), (37, 53)):
            frame = np.full((*size, 3), 128, np.uint8)
            for fit in ('Вписать', 'Заполнить', 'Растянуть'):
                for rotation in ('0', '90', '180', '270'):
                    config = dict(DEFAULT, fit=fit, rotation=rotation, zoom=5., saturation=.5)
                    view = processed_preview(frame, config, 101, 79)
                    self.assertGreater(view.size, 0)
                    self.assertLessEqual(view.shape[0], 79)
                    self.assertLessEqual(view.shape[1], 101)

    def test_region_clips_negative_monitor_coordinates(self):
        monitors = [dict(left=-1920, top=0, width=3840, height=1080),
                    dict(left=-1920, top=0, width=1920, height=1080)]
        c = dict(DEFAULT, capture_kind='Область', left='-2000', top='-20', width='500', height='300')
        self.assertEqual(capture_region(c, monitors), dict(left=-1920, top=0, width=420, height=280))
        c['left'] = '9000'
        with self.assertRaises(ValueError):
            capture_region(c, monitors)

    def test_settings_reject_port_conflicts_and_invalid_audio(self):
        self.assertEqual(validate_config(dict(DEFAULT, role='server'))['fps'], '30')
        for changes in ({'video_port': '5001'}, {'audio_port': '5002'}, {'block_ms': '0'},
                        {'fps': '0'}, {'zoom': float('nan')}, {'system_enabled': False, 'mic_enabled': False}):
            with self.assertRaises(ValueError):
                validate_config(dict(DEFAULT, role='server', **changes))


if __name__ == '__main__':
    unittest.main()
