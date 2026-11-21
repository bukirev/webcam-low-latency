"""Windows developer benchmark: real Tk uploads, hidden window, same input frames.

This measures CPU time in this process, not Task Manager CPU on another computer.
It excludes visible desktop composition and network delivery.
"""
import ast
import json
from pathlib import Path
import subprocess
import sys
import time
import tkinter as tk

import cv2
import numpy as np
from PIL import Image, ImageTk

ROOT = Path(__file__).resolve().parents[1]
sys.path[:0] = [str(ROOT), str(ROOT / 'tests')]
from test_client_performance import CameraRenderTests
from video_display import FramePresenter

root = tk.Tk()
root.withdraw()
old_source = subprocess.check_output(['git', 'show', 'v4.0.0:camera.py'], cwd=ROOT).decode('utf-8')
old_render = next(n for n in ast.parse(old_source).body if isinstance(n, ast.FunctionDef) and n.name == 'render')
rng = np.random.default_rng(42)
frames = [rng.integers(0, 256, (720, 1280, 3), dtype=np.uint8) for _ in range(2)]
threads = cv2.getNumThreads()
results = {'opencv_default_threads': threads, 'camera': {}}
try:
    for name, old, thread_count in [('v4_default', True, threads), ('v4_one_thread', True, 1), ('v4_1', False, 1)]:
        cv2.setNumThreads(thread_count)
        test = CameraRenderTests()
        test.setUp()
        namespace = test.ns
        if old:
            exec(compile(ast.Module(body=[old_render], type_ignores=[]), 'v4_camera.py', 'exec'), namespace)
        view = tk.Label(root)
        view.pack()
        view.winfo_width = lambda: 960
        view.winfo_height = lambda: 540
        namespace.update(view=view, Image=Image, ImageTk=ImageTk, presenter=FramePresenter(view))
        # Prime the photo storage and OpenCV worker pool before measuring.
        for frame in frames:
            namespace['latest'] = frame
            namespace['render']()
            root.update_idletasks()
        begin = time.process_time()
        wall = time.perf_counter()
        for i in range(90):
            namespace['latest'] = frames[(i // 3) % 2]
            namespace['render']()
            root.update_idletasks()
        results['camera'][name] = dict(cpu_ms=round((time.process_time()-begin)*1000, 2),
                                       wall_ms=round((time.perf_counter()-wall)*1000, 2),
                                       polls=90, source_frames=30)
        view.destroy()
        del namespace
    cv2.setNumThreads(1)
    image = rng.integers(0, 256, (1080, 1920, 3), dtype=np.uint8)
    _, jpg = cv2.imencode('.jpg', image, [cv2.IMWRITE_JPEG_QUALITY, 80])
    results['jpeg_1080p'] = {}
    for scale, flag in [(1, cv2.IMREAD_COLOR), (2, cv2.IMREAD_REDUCED_COLOR_2), (4, cv2.IMREAD_REDUCED_COLOR_4)]:
        for _ in range(3): cv2.imdecode(jpg, flag)
        begin = time.process_time()
        for _ in range(50): cv2.imdecode(jpg, flag)
        results['jpeg_1080p'][str(scale)] = {'cpu_ms_per_frame': round((time.process_time()-begin)*1000/50, 3)}
finally:
    root.destroy()
    cv2.setNumThreads(threads)
print(json.dumps(results, indent=2))
target = ROOT / 'release' / 'client-benchmark.json'
target.parent.mkdir(exist_ok=True)
target.write_text(json.dumps(results, indent=2), encoding='utf-8')
