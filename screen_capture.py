# Python-захват видимых пикселей через mss, с отдельным рисованием курсора.
# Область задана физическими координатами виртуального рабочего стола, X/Y могут
# быть отрицательными. Контракты перенесены в Go: docs/modules/screen-capture.md.
"""Desktop capture is independent of camera capture and its settings."""
import ctypes
from ctypes import wintypes
import time

import cv2
import mss
import numpy as np


def monitor_list():
    with mss.mss() as capture:
        return [dict(m) for m in capture.monitors]


def window_list():
    user = ctypes.windll.user32
    result = []
    callback_type = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)

    @callback_type
    def callback(hwnd, _):
        length = user.GetWindowTextLengthW(hwnd)
        if length and user.IsWindowVisible(hwnd):
            text = ctypes.create_unicode_buffer(length + 1)
            user.GetWindowTextW(hwnd, text, length + 1)
            result.append(f'{int(hwnd)}: {text.value}')
        return True

    user.EnumWindows(callback, 0)
    return result


def capture_region(config, monitors):
    desktop = monitors[0]
    kind = config['capture_kind']
    if kind == 'Область':
        region = {key: int(config[key]) for key in ('left', 'top', 'width', 'height')}
    elif kind == 'Окно (видимая область)':
        hwnd = int(config['window'].split(':', 1)[0])
        user = ctypes.windll.user32
        if not user.IsWindow(wintypes.HWND(hwnd)) or user.IsIconic(wintypes.HWND(hwnd)):
            raise ValueError('Выбранное окно закрыто или свёрнуто')
        rect = wintypes.RECT()
        if not user.GetWindowRect(wintypes.HWND(hwnd), ctypes.byref(rect)):
            raise ValueError('Не удалось определить границы окна')
        region = dict(left=rect.left, top=rect.top, width=rect.right - rect.left, height=rect.bottom - rect.top)
    elif kind == 'Все мониторы':
        region = dict(desktop)
    else:
        index = int(config['monitor'].split(':', 1)[0])
        if not 1 <= index < len(monitors):
            raise ValueError('Выбранный монитор недоступен. Обновите список устройств.')
        region = dict(monitors[index])
    left = max(region['left'], desktop['left'])
    top = max(region['top'], desktop['top'])
    right = min(region['left'] + region['width'], desktop['left'] + desktop['width'])
    bottom = min(region['top'] + region['height'], desktop['top'] + desktop['height'])
    if right <= left or bottom <= top:
        raise ValueError('Область находится вне рабочего стола')
    return dict(left=left, top=top, width=right - left, height=bottom - top)


# Владелец GDI-ресурсов курсора. Native handles освобождаются close независимо
# от существования Python-объекта; hotspot участвует в смещении рисунка.
class CursorPainter:
    """Draw the actual Windows cursor, including hotspot, into a small GDI patch."""
    class CURSORINFO(ctypes.Structure):
        _fields_ = [('cbSize', wintypes.DWORD), ('flags', wintypes.DWORD),
                    ('hCursor', wintypes.HANDLE), ('ptScreenPos', wintypes.POINT)]

    class ICONINFO(ctypes.Structure):
        _fields_ = [('fIcon', wintypes.BOOL), ('xHotspot', wintypes.DWORD),
                    ('yHotspot', wintypes.DWORD), ('hbmMask', wintypes.HBITMAP),
                    ('hbmColor', wintypes.HBITMAP)]

    class BITMAPINFOHEADER(ctypes.Structure):
        _fields_ = [('biSize', wintypes.DWORD), ('biWidth', wintypes.LONG),
                    ('biHeight', wintypes.LONG), ('biPlanes', wintypes.WORD),
                    ('biBitCount', wintypes.WORD), ('biCompression', wintypes.DWORD),
                    ('biSizeImage', wintypes.DWORD), ('biXPelsPerMeter', wintypes.LONG),
                    ('biYPelsPerMeter', wintypes.LONG), ('biClrUsed', wintypes.DWORD),
                    ('biClrImportant', wintypes.DWORD)]

    def __init__(self):
        self.user = ctypes.WinDLL('user32', use_last_error=True)
        self.gdi = ctypes.WinDLL('gdi32', use_last_error=True)
        self.gdi.CreateCompatibleDC.argtypes = [wintypes.HDC]
        self.gdi.CreateCompatibleDC.restype = wintypes.HDC
        self.gdi.CreateDIBSection.argtypes = [wintypes.HDC, ctypes.c_void_p, wintypes.UINT,
                                             ctypes.POINTER(ctypes.c_void_p), wintypes.HANDLE, wintypes.DWORD]
        self.gdi.CreateDIBSection.restype = wintypes.HBITMAP
        self.gdi.SelectObject.argtypes = [wintypes.HDC, wintypes.HANDLE]
        self.gdi.SelectObject.restype = wintypes.HANDLE
        self.gdi.DeleteObject.argtypes = [wintypes.HANDLE]
        self.gdi.DeleteDC.argtypes = [wintypes.HDC]
        self.user.GetCursorInfo.argtypes = [ctypes.POINTER(self.CURSORINFO)]
        self.user.GetIconInfo.argtypes = [wintypes.HANDLE, ctypes.POINTER(self.ICONINFO)]
        self.user.DrawIconEx.argtypes = [wintypes.HDC, ctypes.c_int, ctypes.c_int, wintypes.HANDLE,
                                       ctypes.c_int, ctypes.c_int, wintypes.UINT, wintypes.HBRUSH, wintypes.UINT]
        self.dc = self.gdi.CreateCompatibleDC(None)
        header = self.BITMAPINFOHEADER()
        header.biSize = ctypes.sizeof(header)
        header.biWidth = 128
        header.biHeight = -128
        header.biPlanes = 1
        header.biBitCount = 32
        bits = ctypes.c_void_p()
        self.bitmap = self.gdi.CreateDIBSection(self.dc, ctypes.byref(header), 0, ctypes.byref(bits), None, 0)
        if not self.dc or not self.bitmap or not bits.value:
            self.close()
            raise OSError('Не удалось создать изображение указателя')
        self.previous = self.gdi.SelectObject(self.dc, self.bitmap)
        self.pixels = np.ctypeslib.as_array((ctypes.c_ubyte * (128 * 128 * 4)).from_address(bits.value)).reshape(128, 128, 4)

    def draw(self, frame, region):
        cursor = self.CURSORINFO()
        cursor.cbSize = ctypes.sizeof(cursor)
        if not self.user.GetCursorInfo(ctypes.byref(cursor)) or not cursor.flags & 1:
            return
        icon = self.ICONINFO()
        if not self.user.GetIconInfo(cursor.hCursor, ctypes.byref(icon)):
            return
        try:
            x = cursor.ptScreenPos.x - icon.xHotspot - region['left']
            y = cursor.ptScreenPos.y - icon.yHotspot - region['top']
            left, top = max(0, x), max(0, y)
            right, bottom = min(frame.shape[1], x + 128), min(frame.shape[0], y + 128)
            if right <= left or bottom <= top:
                return
            w, h = right - left, bottom - top
            self.pixels[:h, :w] = frame[top:bottom, left:right]
            self.user.DrawIconEx(self.dc, x - left, y - top, cursor.hCursor, 0, 0, 0, None, 3)
            self.gdi.GdiFlush()
            frame[top:bottom, left:right] = self.pixels[:h, :w]
        finally:
            if icon.hbmMask:
                self.gdi.DeleteObject(icon.hbmMask)
            if icon.hbmColor:
                self.gdi.DeleteObject(icon.hbmColor)

    def close(self):
        if getattr(self, 'previous', None):
            self.gdi.SelectObject(self.dc, self.previous)
        if getattr(self, 'bitmap', None):
            self.gdi.DeleteObject(self.bitmap)
        if getattr(self, 'dc', None):
            self.gdi.DeleteDC(self.dc)


# mss session и CursorPainter живут внутри worker. Новые BGR arrays публикуются
# как кадры для чтения; stop.wait позволяет отменить FPS delay без блокировки Tk.
class ScreenCapture:
    def __init__(self, stop, get_config, publish):
        self.stop = stop
        self.get_config = get_config
        self.publish = publish
        self.status = 'Экран: запуск…'
        self.fps = 0.

    def run(self):
        while not self.stop.is_set():
            painter = None
            try:
                with mss.mss() as capture:
                    painter = CursorPainter()
                    stamp = time.monotonic()
                    count = 0
                    while not self.stop.is_set():
                        begin = time.monotonic()
                        c = self.get_config()
                        if c['pause_video']:
                            # Remove the last frame so a new subscriber cannot see it.
                            self.publish(None)
                            self.status = 'Экран: пауза'
                            self.stop.wait(.1)
                            continue
                        region = capture_region(c, capture.monitors)
                        bgra = np.array(capture.grab(region))
                        if c['cursor']:
                            painter.draw(bgra, region)
                        frame = cv2.cvtColor(bgra, cv2.COLOR_BGRA2BGR)
                        max_width = int(c['max_width'])
                        if max_width and frame.shape[1] > max_width:
                            height = max(1, round(frame.shape[0] * max_width / frame.shape[1]))
                            frame = cv2.resize(frame, (max_width, height), interpolation=cv2.INTER_AREA)
                        self.publish(frame)
                        count += 1
                        elapsed = time.monotonic() - stamp
                        if elapsed >= 1:
                            self.fps = count / elapsed
                            self.status = f'Экран: {frame.shape[1]}×{frame.shape[0]} | {self.fps:.1f} FPS'
                            stamp = time.monotonic()
                            count = 0
                        self.stop.wait(max(0, 1 / int(c['fps']) - (time.monotonic() - begin)))
            except Exception as e:
                self.publish(None)
                self.status = 'Ошибка захвата: ' + str(e)
                self.stop.wait(1)
            finally:
                if painter:
                    painter.close()
