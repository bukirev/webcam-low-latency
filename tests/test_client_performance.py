"""Regression tests for duplicate rendering and client-side decode throttling."""
import ast
from pathlib import Path
import threading
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import numpy as np

from network import Receiver, Sender, decode_jpeg
from test_media import free_port


class CameraRenderTests(unittest.TestCase):
    def setUp(self):
        # Load the actual render function without opening a camera or Tk mainloop.
        tree = ast.parse(Path('camera.py').read_text(encoding='utf-8'))
        defaults = next(n for n in tree.body if isinstance(n, ast.Assign)
                        and any(isinstance(t, ast.Name) and t.id == 'DEFAULT' for t in n.targets))
        render = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'render')
        class Status:
            text = ''
            def get(self): return self.text
            def set(self, value): self.text = value
        class Presenter:
            shows = 0
            def show(self, frame): self.shows += 1
            def clear(self, text): pass
        self.size = [640, 480]
        self.minimized = False
        self.ns = dict(time=time, cv2=__import__('cv2'), lock=threading.Lock(), displayed=None,
                       CAMERA_DEFAULT=__import__('media_settings').CAMERA_DEFAULT,
                       media_started=True,
                       latest=np.zeros((480, 640, 3), np.uint8),
                       last_render_frame=None, last_render_key=None, last_placeholder=None,
                       last_status_time=0., minimized=False, MODE='client', info='', actual=0,
                       net=SimpleNamespace(status='test'), status=Status(), presenter=Presenter(),
                       root=SimpleNamespace(state=lambda: 'iconic' if self.minimized else 'normal', after=lambda *a: None),
                       view=SimpleNamespace(winfo_width=lambda: self.size[0], winfo_height=lambda: self.size[1], configure=lambda **kw: None))
        exec(compile(ast.Module(body=[defaults, render], type_ignores=[]), 'camera.py', 'exec'), self.ns)
        self.ns['config'] = dict(self.ns['DEFAULT'], preview=True, receive_fps='0', decode_scale='1')

    def test_same_frame_is_rendered_once_but_view_changes_are_immediate(self):
        for _ in range(20): self.ns['render']()
        self.assertEqual(self.ns['presenter'].shows, 1)
        self.ns['latest'] = self.ns['latest'].copy()
        self.ns['render']()
        self.assertEqual(self.ns['presenter'].shows, 2)
        self.ns['config'] = dict(self.ns['config'], brightness=10)
        self.ns['render']()
        self.assertEqual(self.ns['presenter'].shows, 3)
        self.size[:] = [320, 240]
        self.ns['render']()
        self.assertEqual(self.ns['presenter'].shows, 4)
        self.ns['config'] = dict(self.ns['config'], host='192.0.2.1')
        self.ns['render']()
        self.assertEqual(self.ns['presenter'].shows, 4)

    def test_minimized_camera_suspends_decoding_and_resumes(self):
        self.minimized = True
        self.ns['render']()
        self.assertFalse(self.ns['config']['_receive_visible'])
        self.assertEqual(self.ns['presenter'].shows, 0)
        self.minimized = False
        self.ns['last_status_time'] = 0
        self.ns['render']()
        self.assertTrue(self.ns['config']['_receive_visible'])
        self.assertEqual(self.ns['presenter'].shows, 1)


class DecodeTests(unittest.TestCase):
    def test_reduced_jpeg_decoder_preserves_color_and_dimensions(self):
        import cv2
        image = np.full((480, 640, 3), [40, 100, 200], np.uint8)
        ok, jpg = cv2.imencode('.jpg', image)
        self.assertTrue(ok)
        for scale in (1, 2, 4):
            decoded = decode_jpeg(jpg.tobytes(), scale)
            self.assertEqual(decoded.shape, (480 // scale, 640 // scale, 3))
            np.testing.assert_allclose(decoded.mean(axis=(0, 1)), [40, 100, 200], atol=3)

    def test_live_fps_scale_and_hidden_window_controls(self):
        stop = threading.Event()
        config = dict(host='127.0.0.1', quality=80, receive_fps='20', decode_scale='2', _receive_visible=False)
        outputs = []
        port = free_port()
        sender = Sender(stop, lambda: np.full((100, 200, 3), 100, np.uint8), lambda: config, port=port)
        receiver = Receiver(stop, lambda: config, lambda f: outputs.append(f) if f is not None else None, port=port)
        threads = [threading.Thread(target=s.run) for s in (sender, receiver)]
        for thread in threads: thread.start()
        try:
            time.sleep(.12)
            self.assertFalse(outputs)
            config['_receive_visible'] = True
            begin = time.monotonic()
            deadline = begin + 2
            while len(outputs) < 5 and time.monotonic() < deadline:
                time.sleep(.01)
            self.assertGreaterEqual(len(outputs), 5)
            self.assertGreater(time.monotonic()-begin, .12)
            self.assertEqual(outputs[-1].shape, (50, 100, 3))
            config['decode_scale'] = '1'
            deadline = time.monotonic() + 1
            while outputs[-1].shape != (100, 200, 3) and time.monotonic() < deadline:
                time.sleep(.01)
            self.assertEqual(outputs[-1].shape, (100, 200, 3))
        finally:
            stop.set()
            for thread in threads:
                thread.join(3)
                self.assertFalse(thread.is_alive())


class ScreenUITests(unittest.TestCase):
    def test_window_attributes_and_uploads_are_not_repeated_per_poll(self):
        import tkinter as tk
        import screen_window as screen
        root = tk.Tk()
        root.withdraw()
        root.after = lambda *a: None
        try:
            with patch.object(screen.ScreenWindow, 'refresh_devices', lambda self: None), \
                    patch.object(screen.Path, 'read_text', side_effect=FileNotFoundError):
                app = screen.ScreenWindow(root, 'client')
            app.view.winfo_width = lambda: 640
            app.view.winfo_height = lambda: 480
            app.session = SimpleNamespace(config=dict(screen.DEFAULT, role='client'),
                                          lock=threading.Lock(), frame=np.zeros((480, 640, 3), np.uint8),
                                          frame_time=time.monotonic(), capture=None, video=None, audio=None)
            with patch.object(root, 'attributes', wraps=root.attributes) as attributes, \
                    patch.object(app.presenter, 'show') as upload:
                for _ in range(12): app.render()
                self.assertEqual(upload.call_count, 1)
                self.assertEqual(attributes.call_count, 0)
                app.vars['brightness'].set(12)
                app.vars['receive_fps'].set('20')
                app.render()
                self.assertEqual(upload.call_count, 2)
                self.assertEqual(app.session.config['receive_fps'], '20')
                app.vars['topmost'].set(True)
                self.assertEqual(attributes.call_count, 1)
        finally:
            root.destroy()


if __name__ == '__main__':
    unittest.main()
