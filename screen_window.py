# Python-окно экрана/звука: ScreenSession владеет workers и остановкой; UI отдельно
# хранит черновик и кэш обработанного кадра. Управляемый режим подчиняется панели.
# Подробности: docs/legacy-python.md; сравнение вывода: docs/modules/render.md.
"""Independent screen/audio window. No capture starts until the user presses Start."""
import json
import os
from pathlib import Path
import sys
import threading
import time
import tkinter as tk
from tkinter import messagebox, ttk

import cv2
import mss
import numpy as np
from PIL import Image, ImageTk

from audio_stream import AudioReceiver, AudioSender, list_devices
from network import Receiver, Sender
from screen_capture import ScreenCapture, monitor_list, window_list
from video_display import FramePresenter
from media_settings import SCREEN_DEFAULT, APP_VERSION, validate_settings, write_json
from panel_control import ManagedClient

DEFAULT = dict(SCREEN_DEFAULT)
DEFAULT.update(receive_fps='0', decode_scale='1')
ROLES = {'local': 'Локальный просмотр', 'server': 'Передатчик', 'client': 'Приёмник'}
LIVE_KEYS = ('system_gain', 'mic_gain', 'send_mute', 'receive_mute', 'volume', 'pause_video')
LIVE_KEYS += ('receive_fps', 'decode_scale', '_receive_visible')


def validate_config(config):
    c = dict(config)
    client=c.get('role')=='client'
    for name in ('video_port', 'audio_port'):
        value = int(c[name])
        if not 1024 <= value <= 65535 or value == 5001:
            raise ValueError('Порты экрана и звука: 1024–65535, кроме порта камеры 5001.')
        c[name] = str(value)
    if c['video_port'] == c['audio_port']:
        raise ValueError('Для экрана и звука нужны разные порты.')
    for name, low, high in [('fps', 1, 120), ('max_width', 0, 7680), ('width', 1, 16384),
                            ('height', 1, 16384), ('left', -32768, 32768), ('top', -32768, 32768)]:
        if client: continue
        value = int(c[name])
        if not low <= value <= high:
            raise ValueError(f'{name}: допустимый диапазон {low}–{high}.')
        c[name] = str(value)
    for name, choices in [('sample_rate', ('16000', '24000', '44100', '48000')),
                          ('channels', ('1', '2')), ('block_ms', ('5', '10', '20')),
                          ('buffer_ms', ('10', '20', '40', '80', '120', '200')),
                          ('receive_fps', ('0', '15', '20', '25', '30', '60', '120')),
                          ('decode_scale', ('1', '2', '4'))]:
        if client and name in ('sample_rate','channels'): continue
        if c[name] not in choices:
            raise ValueError('Недопустимое значение: ' + name)
    for name, low, high in [('quality', 30, 95), ('zoom', 1, 5), ('volume', 0, 200),
                            ('system_gain', 0, 200), ('mic_gain', 0, 300),
                            ('brightness', -100, 100), ('contrast', .2, 3),
                            ('saturation', 0, 2), ('gamma', .2, 3), ('sharpness', 0, 2)]:
        if client and name in ('quality','system_gain','mic_gain'): continue
        value = float(c[name])
        if not np.isfinite(value) or not low <= value <= high:
            raise ValueError('Недопустимое значение: ' + name)
        c[name] = value
    if not c['video_enabled'] and not c['audio_enabled']:
        raise ValueError('Включите передачу экрана или звука.')
    if c['audio_enabled'] and c['role'] != 'client' and not (c['system_enabled'] or c['mic_enabled']):
        raise ValueError('Выберите системный звук, микрофон или оба источника.')
    if c['role'] not in ROLES:
        raise ValueError('Неизвестный режим работы.')
    if c['capture_kind'] == 'Окно (видимая область)' and not c['window'] and c['role'] != 'client':
        raise ValueError('Выберите окно для захвата.')
    return c


# Один сеанс владеет своим stop event, workers и frame. Изменения, требующие
# другого backend/device, проходят остановку и создание нового Session.
class ScreenSession:
    def __init__(self, config):
        if config['role']=='client': cv2.setNumThreads(1)
        self.config = dict(config)
        self.stop = threading.Event()
        self.lock = threading.Lock()
        self.frame = None
        self.frame_time = 0.
        self.capture = self.video = self.audio = None
        self.threads = []
        role = config['role']
        if config['video_enabled']:
            if role == 'client':
                self.video = Receiver(self.stop, self.get_config, self.publish,
                                      port=int(config['video_port']), magic=b'WS40')
            else:
                self.capture = ScreenCapture(self.stop, self.get_config, self.publish)
                if role == 'server':
                    self.video = Sender(self.stop, self.get_frame, self.get_config,
                                        port=int(config['video_port']), magic=b'WS40')
        if config['audio_enabled']:
            self.audio = (AudioReceiver if role == 'client' else AudioSender)(self.stop, self.get_config)
        for service in (self.capture, self.video, self.audio):
            if service:
                thread = threading.Thread(target=service.run, daemon=True)
                self.threads.append(thread)
                thread.start()

    def get_config(self):
        return self.config

    def publish(self, frame):
        with self.lock:
            self.frame = frame
            self.frame_time = time.monotonic()

    def get_frame(self):
        with self.lock:
            return self.frame

    def running(self):
        return any(t.is_alive() for t in self.threads)


# Обработка относится к локальному виду и не должна менять опубликованный
# source frame для сетевого sender. Render кеширует итог по frame/config/size.
def processed_preview(frame, config, width, height):
    f = frame
    rotation = int(config['rotation'])
    if rotation:
        f = cv2.rotate(f, {90: cv2.ROTATE_90_CLOCKWISE, 180: cv2.ROTATE_180,
                           270: cv2.ROTATE_90_COUNTERCLOCKWISE}[rotation])
    if config['mirror']:
        f = cv2.flip(f, 1)
    h, w = f.shape[:2]
    zw, zh = max(1, int(w / config['zoom'])), max(1, int(h / config['zoom']))
    f = f[(h - zh) // 2:(h - zh) // 2 + zh, (w - zw) // 2:(w - zw) // 2 + zw]
    width, height = max(2, width), max(2, height)
    h, w = f.shape[:2]
    if config['fit'] == 'Растянуть':
        dw, dh = width, height
    else:
        factor = (max if config['fit'] == 'Заполнить' else min)(width / w, height / h)
        # Crop before resizing to avoid enormous temporary images at high zoom.
        if config['fit'] == 'Заполнить':
            crop_w, crop_h = min(w, max(1, round(width / factor))), min(h, max(1, round(height / factor)))
            f = f[(h - crop_h) // 2:(h - crop_h) // 2 + crop_h,
                  (w - crop_w) // 2:(w - crop_w) // 2 + crop_w]
            dw, dh = width, height
        else:
            dw, dh = max(1, int(w * factor)), max(1, int(h * factor))
    if f.shape[1] != dw or f.shape[0] != dh:
        f = cv2.resize(f, (dw, dh), interpolation=cv2.INTER_LINEAR)
    if config['brightness'] or config['contrast'] != 1 or config['gamma'] != 1:
        values = np.arange(256, dtype=np.float32)
        lut = np.clip(((values / 255) ** (1 / config['gamma']) * 255 - 128)
                      * config['contrast'] + 128 + config['brightness'], 0, 255).astype('uint8')
        f = cv2.LUT(f, lut)
    if config['saturation'] != 1:
        hsv = cv2.cvtColor(f, cv2.COLOR_BGR2HSV)
        hsv[:, :, 1] = np.clip(hsv[:, :, 1].astype(np.float32) * config['saturation'], 0, 255).astype('uint8')
        f = cv2.cvtColor(hsv, cv2.COLOR_HSV2BGR)
    if config['sharpness']:
        f = cv2.addWeighted(f, 1 + config['sharpness'], cv2.GaussianBlur(f, (0, 0), 1), -config['sharpness'], 0)
    return f


# В controlled mode источником применённых параметров служит Dashboard.
# Не добавляйте вторую независимо действующую кнопку Apply в managed preview.
class ScreenWindow:
    def __init__(self, root, role, initial=None, controlled=False):
        self.root = root
        self.controlled=controlled
        self.role = role if role in ROLES else 'local'
        self.settings_path = Path(f'settings_screen_{self.role}.json')
        try:
            saved = json.loads(self.settings_path.read_text(encoding='utf-8'))
            if not isinstance(saved, dict):
                saved = {}
        except (OSError, ValueError):
            saved = {}
        if initial:
            saved.update(validate_settings('screen',initial,self.role,require_sources=False))
        self.vars = {}
        for name, default in DEFAULT.items():
            value = saved.get(name, default)
            if not isinstance(value, type(default)):
                value = default
            cls = tk.BooleanVar if isinstance(default, bool) else tk.DoubleVar if isinstance(default, float) else tk.StringVar
            self.vars[name] = cls(value=value)
        self.role_var = tk.StringVar(value=ROLES[self.role])
        self.session = None
        self.closing = False
        self.transition = False
        self.pending = None
        self.monitors = []
        self.device_combos = {}
        self.scale_trace_ids=[]
        self.displayed = None
        self.last_render_key = None
        self.full = False
        self.panel_visible = True
        self.minimized = False
        self.last_status_time = 0.
        self.last_topmost = None
        self.last_placeholder = None
        self.ui_config = self.snapshot()
        root.title('Экран и звук '+APP_VERSION+' — ' + ROLES[self.role])
        try:
            root.geometry(saved.get('geometry', '1250x800'))
        except tk.TclError:
            root.geometry('1250x800')
        root.minsize(760, 480)
        root.report_callback_exception = self.callback_error

        toolbar = ttk.Frame(root, padding=6)
        toolbar.pack(fill='x')
        if not controlled: ttk.Label(toolbar, text='Режим:').pack(side='left')
        self.role_combo=ttk.Combobox(toolbar, textvariable=self.role_var, values=list(ROLES.values()),
                     state='readonly', width=21)
        self.role_combo.bind('<<ComboboxSelected>>',lambda e:self.rebuild_controls())
        if not controlled: self.role_combo.pack(side='left', padx=6)
        self.start_button = ttk.Button(toolbar, text='▶ Старт / применить', command=self.apply)
        if not controlled:
            self.start_button.pack(side='left', padx=3)
            ttk.Button(toolbar, text='■ Стоп', command=self.stop_session).pack(side='left', padx=3)
            ttk.Button(toolbar, text='Настройки (F2)', command=self.toggle_panel).pack(side='right')
        else: ttk.Label(toolbar,text='Управление и настройки — в единой панели').pack(side='left')
        ttk.Button(toolbar, text='Полный экран (F11)', command=self.fullscreen).pack(side='right', padx=3)
        ttk.Button(toolbar, text='Снимок (F5)', command=self.shot).pack(side='right', padx=3)
        body = ttk.Frame(root)
        body.pack(fill='both', expand=True)
        self.panel = ttk.Notebook(body, width=335)
        self.panel.pack(side='right', fill='y', padx=4)
        self.view = tk.Label(body, bg='#101318', fg='#b8c0cc', text='Выберите режим и нажмите «Старт»', anchor='center')
        self.view.pack(side='left', fill='both', expand=True)
        self.presenter = FramePresenter(self.view)
        self.tabs = {}
        for name in ('Связь', 'Экран', 'Звук', 'Вид'):
            outer = ttk.Frame(self.panel)
            self.panel.add(outer, text=name)
            canvas = tk.Canvas(outer, highlightthickness=0, width=320)
            scroll = ttk.Scrollbar(outer, orient='vertical', command=canvas.yview)
            scroll.pack(side='right', fill='y')
            canvas.pack(side='left', fill='both', expand=True)
            canvas.configure(yscrollcommand=scroll.set)
            inner = ttk.Frame(canvas, padding=8)
            win = canvas.create_window(0, 0, window=inner, anchor='nw')
            inner.bind('<Configure>', lambda e, c=canvas: c.configure(scrollregion=c.bbox('all')))
            canvas.bind('<Configure>', lambda e, c=canvas, w=win: c.itemconfigure(w, width=e.width))
            canvas.bind('<MouseWheel>', lambda e, c=canvas: c.yview_scroll(-int(e.delta / 120), 'units'))
            inner.bind('<MouseWheel>', lambda e, c=canvas: c.yview_scroll(-int(e.delta / 120), 'units'))
            self.tabs[name] = inner

        self.build_controls()
        self.bind_scroll()
        footer = ttk.Frame(root, padding=5)
        footer.pack(fill='x')
        self.video_status = tk.StringVar(value='Остановлено. Камера работает независимо в своём окне.')
        self.audio_status = tk.StringVar(value='')
        ttk.Label(footer, textvariable=self.video_status, wraplength=1180).pack(anchor='w')
        ttk.Label(footer, textvariable=self.audio_status, wraplength=1180).pack(anchor='w')
        meter_row = ttk.Frame(footer)
        meter_row.pack(fill='x')
        ttk.Label(meter_row, text='Уровень звука:').pack(side='left')
        self.meter = ttk.Progressbar(meter_row, maximum=100, length=180)
        self.meter.pack(side='left', padx=5)
        self.meter_label = ttk.Label(meter_row, text='—')
        self.meter_label.pack(side='left')
        self.refresh_devices()
        for var in self.vars.values():
            var.trace_add('write', self.update_ui_config)
        self.update_ui_config()
        root.bind('<F2>', lambda e: self.toggle_panel())
        root.bind('<F11>', lambda e: self.fullscreen())
        root.bind('<F5>', lambda e: self.shot())
        root.bind('<Escape>', lambda e: self.fullscreen() if self.full else None)
        root.protocol('WM_DELETE_WINDOW', self.close)
        self.render()

    def note(self, parent, text):
        ttk.Label(parent, text=text, wraplength=290, foreground='#626d7b').pack(anchor='w', pady=5)

    def heading(self, parent, text):
        ttk.Separator(parent).pack(fill='x', pady=(10, 5))
        ttk.Label(parent, text=text, font=('Segoe UI', 10, 'bold')).pack(anchor='w', pady=(0, 4))

    def entry(self, parent, label, key):
        ttk.Label(parent, text=label).pack(anchor='w')
        ttk.Entry(parent, textvariable=self.vars[key]).pack(fill='x', pady=(0, 6))

    def combo(self, parent, label, key, values, editable=False):
        ttk.Label(parent, text=label).pack(anchor='w')
        widget = ttk.Combobox(parent, textvariable=self.vars[key], values=values,
                              state='normal' if editable else 'readonly')
        widget.pack(fill='x', pady=(0, 6))
        return widget

    def check(self, parent, text, key):
        ttk.Checkbutton(parent, text=text, variable=self.vars[key]).pack(anchor='w', pady=2)

    def scale(self, parent, text, key, low, high):
        row = ttk.Frame(parent)
        row.pack(fill='x', pady=(5, 0))
        ttk.Label(row, text=text).pack(side='left')
        value = ttk.Label(row)
        value.pack(side='right')
        def refresh(*_):
            try:
                value.configure(text=f'{self.vars[key].get():.2f}')
            except tk.TclError:
                pass
        trace=self.vars[key].trace_add('write', refresh)
        self.scale_trace_ids.append((self.vars[key],trace))
        refresh()
        ttk.Scale(parent, variable=self.vars[key], from_=low, to=high).pack(fill='x')

    def build_controls(self):
        if self.role_var.get()==ROLES['client']:
            self.build_receiver_controls()
            return
        p = self.tabs['Связь']
        self.heading(p, 'Передача по локальной сети')
        self.entry(p, 'IP передатчика (пусто — автопоиск)', 'host')
        self.check(p, 'Передавать / принимать экран', 'video_enabled')
        self.check(p, 'Передавать / принимать звук', 'audio_enabled')
        self.entry(p, 'UDP-порт экрана', 'video_port')
        self.entry(p, 'UDP-порт звука', 'audio_port')
        self.heading(p, 'Нагрузка приёмника')
        self.combo(p, 'Максимум FPS приёма (0 — без лимита)', 'receive_fps', ['0', '15', '20', '25', '30', '60', '120'])
        self.combo(p, 'Декодирование JPEG в масштабе 1/N', 'decode_scale', ['1', '2', '4'])
        ttk.Button(p, text='Экономия CPU: 20 FPS, JPEG /2', command=self.economy).pack(fill='x', pady=4)
        self.note(p, 'Эти настройки клиента действуют сразу. JPEG /2 уменьшает ширину и высоту изображения вдвое. Лимит FPS отбрасывает лишние кадры без очереди. Звук работает независимо.')
        self.note(p, 'На обоих компьютерах укажите одинаковые порты. Экран: 5002; звук: 5003. Камера использует отдельный порт 5001.')
        self.note(p, 'Сначала запустите передатчик, затем приёмник. При нескольких передатчиках задайте IP вручную.')
        self.note(p, 'Изменения источников и формата вступают в силу после «Старт / применить». Громкость, отключение звука и пауза экрана действуют сразу.')
        ttk.Button(p, text='Обновить устройства и окна', command=self.refresh_devices).pack(fill='x', pady=5)
        ttk.Button(p, text='Сбросить настройки', command=self.reset).pack(fill='x', pady=5)
        self.note(p, 'Локальный режим: просмотр экрана и уровень входящего звука, без воспроизведения на этом же компьютере.')

        p = self.tabs['Экран']
        self.heading(p, 'Источник на передатчике')
        self.combo(p, 'Что захватывать', 'capture_kind', ['Монитор', 'Все мониторы', 'Область', 'Окно (видимая область)'])
        self.device_combos['monitor'] = self.combo(p, 'Монитор / основа выбора области', 'monitor', [])
        self.device_combos['window'] = self.combo(p, 'Окно', 'window', [])
        self.note(p, 'Захват окна передаёт его видимую область рабочего стола. Перекрывающие окна тоже попадут в кадр; выбранное окно должно быть развёрнуто.')
        ttk.Button(p, text='Выбрать область мышью…', command=self.select_region).pack(fill='x', pady=4)
        for key, label in [('left', 'Область: X'), ('top', 'Область: Y'), ('width', 'Ширина области'), ('height', 'Высота области')]:
            self.entry(p, label, key)
        self.check(p, 'Показывать указатель мыши', 'cursor')
        self.check(p, 'Пауза передачи экрана', 'pause_video')
        self.heading(p, 'Качество и нагрузка')
        self.combo(p, 'Максимальная ширина (0 — исходная)', 'max_width', ['0', '640', '960', '1280', '1920', '2560', '3840'], True)
        self.combo(p, 'FPS', 'fps', ['10', '15', '25', '30', '50', '60', '90', '120'], True)
        self.scale(p, 'Качество JPEG', 'quality', 30, 95)
        self.check(p, 'Локальный предпросмотр', 'preview')
        self.note(p, 'Для небольшой задержки: 1280–1920 пикселей, 30/60 FPS, JPEG 75–85, Ethernet. Предпросмотр можно отключить для снижения нагрузки.')

        p = self.tabs['Звук']
        self.heading(p, 'Источники на передатчике')
        self.check(p, 'Системный звук', 'system_enabled')
        self.device_combos['system_device'] = self.combo(p, 'С какого выхода записывать звук', 'system_device', ['По умолчанию'])
        self.scale(p, 'Громкость системного звука, %', 'system_gain', 0, 200)
        self.check(p, 'Микрофон', 'mic_enabled')
        self.device_combos['mic_device'] = self.combo(p, 'Микрофон / вход', 'mic_device', ['По умолчанию'])
        self.scale(p, 'Громкость микрофона, %', 'mic_gain', 0, 300)
        self.check(p, 'Отключить передаваемый звук', 'send_mute')
        self.heading(p, 'Формат и задержка')
        self.combo(p, 'Частота передачи, Гц', 'sample_rate', ['16000', '24000', '44100', '48000'])
        self.combo(p, 'Каналы (1 — моно, 2 — стерео)', 'channels', ['1', '2'])
        self.combo(p, 'Блок захвата / вывода, мс', 'block_ms', ['5', '10', '20'])
        self.combo(p, 'Буфер приёмника, мс', 'buffer_ms', ['10', '20', '40', '80', '120', '200'])
        self.note(p, 'PCM 16 бит без сжатия. Буфер — только часть общей задержки. При щелчках увеличьте блок до 20 мс и буфер до 80 мс.')
        self.heading(p, 'Воспроизведение на приёмнике')
        self.device_combos['output_device'] = self.combo(p, 'Динамики / наушники', 'output_device', ['По умолчанию'])
        self.scale(p, 'Громкость воспроизведения, %', 'volume', 0, 200)
        self.check(p, 'Отключить воспроизведение', 'receive_mute')
        self.note(p, 'Системный звук и микрофон смешиваются. Автоматического подавления эха нет; для работы с микрофоном удобнее наушники.')

        p = self.tabs['Вид']
        self.heading(p, 'Только просмотр в этом окне')
        self.combo(p, 'Масштабирование', 'fit', ['Вписать', 'Заполнить', 'Растянуть'])
        self.scale(p, 'Зум', 'zoom', 1, 5)
        self.combo(p, 'Поворот', 'rotation', ['0', '90', '180', '270'])
        self.check(p, 'Зеркальное изображение', 'mirror')
        self.check(p, 'Поверх остальных окон', 'topmost')
        for key, label, low, high in [('brightness', 'Яркость', -100, 100), ('contrast', 'Контраст', .2, 3),
                                      ('saturation', 'Насыщенность', 0, 2), ('gamma', 'Гамма', .2, 3),
                                      ('sharpness', 'Резкость', 0, 2)]:
            self.scale(p, label, key, low, high)
        self.note(p, 'Изменения вида применяются сразу и не меняют передаваемое изображение. F5 сохраняет снимок текущего вида.')

    def bind_scroll(self):
        for inner in self.tabs.values():
            def bind_wheel(widget,canvas=inner.master):
                def wheel(event):
                    canvas.yview_scroll(-int(event.delta/120),'units')
                    return 'break'
                widget.bind('<MouseWheel>',wheel)
                for child in widget.winfo_children(): bind_wheel(child,canvas)
            bind_wheel(inner)

    def rebuild_controls(self):
        for var,trace in self.scale_trace_ids: var.trace_remove('write',trace)
        self.scale_trace_ids=[]
        for inner in self.tabs.values():
            for child in inner.winfo_children(): child.destroy()
        self.device_combos={}
        self.panel.insert(1,self.tabs['Экран'].master.master)
        self.panel.tab(self.tabs['Экран'].master.master,state='normal')
        self.build_controls()
        self.bind_scroll()
        self.refresh_devices()

    def build_receiver_controls(self):
        p=self.tabs['Связь']
        self.heading(p,'Подключение к передатчику')
        self.entry(p,'IP передатчика (пусто — автопоиск)','host')
        self.check(p,'Принимать экран','video_enabled')
        self.check(p,'Принимать звук','audio_enabled')
        self.entry(p,'UDP-порт экрана','video_port')
        self.entry(p,'UDP-порт звука','audio_port')
        self.heading(p,'Нагрузка приёмника')
        self.combo(p,'Лимит FPS (0 — без лимита)','receive_fps',['0','15','20','25','30','60','120'])
        self.combo(p,'JPEG 1/N (1 — полное качество)','decode_scale',['1','2','4'])
        self.check(p,'Показывать изображение','preview')
        self.panel.hide(1)
        p=self.tabs['Звук']
        self.heading(p,'Воспроизведение на этом компьютере')
        self.device_combos['output_device']=self.combo(p,'Динамики / наушники','output_device',['По умолчанию'])
        self.scale(p,'Громкость, %','volume',0,200)
        self.check(p,'Отключить воспроизведение','receive_mute')
        self.combo(p,'Блок вывода, мс','block_ms',['5','10','20'])
        self.combo(p,'Буфер приёмника, мс','buffer_ms',['10','20','40','80','120','200'])
        ttk.Button(p,text='Обновить устройства вывода',command=self.refresh_devices).pack(fill='x',pady=5)
        p=self.tabs['Вид']
        self.heading(p,'Отображение принятого экрана')
        self.combo(p,'Масштабирование','fit',['Вписать','Заполнить','Растянуть'])
        self.scale(p,'Зум','zoom',1,5)
        self.combo(p,'Поворот','rotation',['0','90','180','270'])
        self.check(p,'Зеркально','mirror')
        self.check(p,'Поверх окон','topmost')
        for key,label,low,high in [('brightness','Яркость',-100,100),('contrast','Контраст',.2,3),
                                    ('saturation','Насыщенность',0,2),('gamma','Гамма',.2,3),('sharpness','Резкость',0,2)]:
            self.scale(p,label,key,low,high)

    def snapshot(self):
        return {name: var.get() for name, var in self.vars.items()}

    def update_ui_config(self, *_):
        try:
            self.ui_config = self.snapshot()
            if self.ui_config['topmost'] != self.last_topmost:
                self.root.attributes('-topmost', self.ui_config['topmost'])
                self.last_topmost = self.ui_config['topmost']
        except (ValueError, tk.TclError):
            pass

    def economy(self):
        self.vars['receive_fps'].set('20')
        self.vars['decode_scale'].set('2')

    def refresh_devices(self):
        messages = []
        if self.role_var.get()!=ROLES['client']:
            try:
                self.monitors = monitor_list()
                monitors = [f'{i}: {m["width"]}×{m["height"]} ({m["left"]}, {m["top"]})'
                            for i, m in enumerate(self.monitors) if i]
                self.device_combos['monitor'].configure(values=monitors)
                old_index = self.vars['monitor'].get().split(':', 1)[0]
                match = next((m for m in monitors if m.split(':', 1)[0] == old_index), None)
                if match or monitors: self.vars['monitor'].set(match or monitors[0])
                self.device_combos['window'].configure(values=window_list())
            except Exception as e:
                messages.append('Мониторы: ' + str(e))
        try:
            devices = list_devices()
            for key, choices in devices.items():
                if key in self.device_combos: self.device_combos[key].configure(values=['По умолчанию'] + choices)
        except Exception as e:
            messages.append('Аудиоустройства: ' + str(e))
        if messages:
            self.video_status.set('; '.join(messages))

    def apply(self):
        try:
            config = self.snapshot()
            config['role'] = next(key for key, value in ROLES.items() if value == self.role_var.get())
            config = validate_config(config)
        except (ValueError, tk.TclError, StopIteration) as e:
            messagebox.showerror('Проверьте настройки', str(e), parent=self.root)
            return
        if self.transition:
            if not self.closing: self.pending=config
            return
        self.pending = config
        self.transition = True
        self.start_button.configure(state='disabled')
        if self.session:
            self.session.stop.set()
        self.wait_stopped(time.monotonic() + 8)

    def wait_stopped(self, deadline):
        if self.session and self.session.running():
            self.video_status.set('Остановка предыдущего сеанса…')
            if time.monotonic() > deadline:
                self.pending = None
                self.transition = False
                self.start_button.configure(state='normal')
                if self.closing:
                    self.root.destroy()
                else:
                    messagebox.showerror('Остановка', 'Аудиодрайвер не завершил работу. Закройте окно и откройте его заново.', parent=self.root)
                return
            self.root.after(50, lambda: self.wait_stopped(deadline))
            return
        self.session = None
        self.displayed = None
        self.last_render_key = None
        self.presenter.clear('Остановлено')
        self.last_placeholder = 'Остановлено'
        self.transition = False
        self.start_button.configure(state='normal')
        if self.closing:
            self.root.destroy()
            return
        if self.pending:
            self.session = ScreenSession(self.pending)
            self.root.title('Экран и звук '+APP_VERSION+' — ' + ROLES[self.pending['role']])
            self.pending = None
            self.video_status.set('Запуск…')
        else:
            self.video_status.set('Остановлено')
            self.audio_status.set('')

    def stop_session(self):
        self.pending = None
        if self.session:
            self.session.stop.set()
        if not self.transition:
            self.transition = True
            self.start_button.configure(state='disabled')
            self.wait_stopped(time.monotonic() + 8)

    def select_region(self):
        try:
            if not self.monitors:
                self.refresh_devices()
            index = int(self.vars['monitor'].get().split(':', 1)[0])
            monitor = self.monitors[index]
            with mss.mss() as capture:
                frame = np.array(capture.grab(monitor))[:, :, :3]
            factor = min(1, 1000 / frame.shape[1], 600 / frame.shape[0])
            w, h = max(1, int(frame.shape[1] * factor)), max(1, int(frame.shape[0] * factor))
            image = ImageTk.PhotoImage(Image.fromarray(cv2.cvtColor(cv2.resize(frame, (w, h)), cv2.COLOR_BGR2RGB)))
        except Exception as e:
            messagebox.showerror('Выбор области', str(e), parent=self.root)
            return
        dialog = tk.Toplevel(self.root)
        dialog.title('Выделите прямоугольник мышью; Esc — отмена')
        dialog.transient(self.root)
        canvas = tk.Canvas(dialog, width=w, height=h, highlightthickness=0, cursor='crosshair')
        canvas.pack()
        canvas.create_image(0, 0, image=image, anchor='nw')
        canvas.image = image
        state = {}
        def begin(e):
            if 'rect' in state:
                canvas.delete(state['rect'])
            state['x'], state['y'] = e.x, e.y
            state['rect'] = canvas.create_rectangle(e.x, e.y, e.x, e.y, outline='#29d9ff', width=2)
        def drag(e):
            if 'rect' in state:
                canvas.coords(state['rect'], state['x'], state['y'], max(0, min(w, e.x)), max(0, min(h, e.y)))
        def finish(e):
            if 'rect' not in state:
                return
            x1, x2 = sorted((state['x'], max(0, min(w, e.x))))
            y1, y2 = sorted((state['y'], max(0, min(h, e.y))))
            if x2 - x1 < 3 or y2 - y1 < 3:
                return
            self.vars['left'].set(str(monitor['left'] + round(x1 / factor)))
            self.vars['top'].set(str(monitor['top'] + round(y1 / factor)))
            self.vars['width'].set(str(round((x2 - x1) / factor)))
            self.vars['height'].set(str(round((y2 - y1) / factor)))
            self.vars['capture_kind'].set('Область')
            dialog.destroy()
        canvas.bind('<ButtonPress-1>', begin)
        canvas.bind('<B1-Motion>', drag)
        canvas.bind('<ButtonRelease-1>', finish)
        dialog.bind('<Escape>', lambda e: dialog.destroy())
        dialog.grab_set()

    def render(self):
        if self.closing:
            return
        try:
            c = self.ui_config
            now = time.monotonic()
            status_due = now-self.last_status_time >= .2
            if status_due:
                self.minimized = self.root.state() == 'iconic'
                self.last_status_time = now
            c = dict(c, _receive_visible=c['preview'] and not self.minimized)
            session = self.session
            frame = None
            if session and not self.transition:
                if any(session.config.get(key) != c[key] for key in LIVE_KEYS):
                    updated = dict(session.config)
                    updated.update({key: c[key] for key in LIVE_KEYS})
                    session.config = updated
                with session.lock:
                    frame = session.frame
                    age = time.monotonic() - session.frame_time
                if session.config['role'] == 'client' and age > .75:
                    frame = None
                if status_due:
                    statuses = [service.status for service in (session.capture, session.video) if service]
                    video_text = ' | '.join(statuses) if statuses else 'Передача экрана отключена'
                    audio_text = session.audio.status if session.audio else 'Звук отключён'
                    if self.video_status.get() != video_text: self.video_status.set(video_text)
                    if self.audio_status.get() != audio_text: self.audio_status.set(audio_text)
                    if not self.minimized:
                        peak = session.audio.peak if session.audio else 0
                        self.meter.configure(value=min(100, peak * 100))
                        self.meter_label.configure(text=f'{20 * np.log10(max(peak, .00001)):.0f} dBFS' + (' — пик' if peak >= .99 else ''))
            if self.minimized:
                self.root.after(100, self.render)
                return
            if not c['preview'] or frame is None:
                text = 'Предпросмотр отключён' if not c['preview'] else ('Ожидание изображения…' if session else 'Выберите режим и нажмите «Старт»')
                if c['pause_video'] and session and session.config['role'] != 'client':
                    text = 'Передача экрана на паузе'
                if text != self.last_placeholder:
                    self.presenter.clear(text)
                    self.last_placeholder = text
                self.displayed = None
                self.last_render_key = None
            else:
                size = self.view.winfo_width(), self.view.winfo_height()
                appearance = tuple(c[k] for k in ('fit', 'zoom', 'rotation', 'mirror', 'brightness', 'contrast', 'saturation', 'gamma', 'sharpness'))
                key = id(frame), size, appearance
                if key != self.last_render_key:
                    self.displayed = processed_preview(frame, c, *size)
                    self.presenter.show(self.displayed)
                    self.last_placeholder = None
                    # Retain the source so Python cannot reuse its id before the next render.
                    self.last_frame = frame
                    self.last_render_key = key
        except (ValueError, tk.TclError):
            pass
        self.root.after(15, self.render)

    def toggle_panel(self):
        if self.controlled and not self.panel_visible: return
        self.panel_visible = not self.panel_visible
        if self.panel_visible:
            self.panel.pack(side='right', fill='y', padx=4, before=self.view)
        else:
            self.panel.pack_forget()

    def fullscreen(self):
        self.full = not self.full
        self.root.attributes('-fullscreen', self.full)

    def shot(self):
        if self.displayed is not None:
            folder = Path('snapshots')
            folder.mkdir(exist_ok=True)
            target = folder / ('screen_' + time.strftime('%Y%m%d_%H%M%S') + f'_{time.time_ns() % 1000000}.png')
            ok, data = cv2.imencode('.png', self.displayed)
            if ok:
                target.write_bytes(data.tobytes())

    def reset(self):
        for name, value in DEFAULT.items():
            self.vars[name].set(value)
        self.refresh_devices()

    def callback_error(self, kind, value, traceback):
        messagebox.showerror('Ошибка', str(value), parent=self.root)

    def close(self):
        try:
            config = self.snapshot()
            config['geometry'] = self.root.geometry()
            write_json(self.settings_path,config)
        except (OSError, ValueError, tk.TclError) as e:
            messagebox.showerror('Настройки', 'Не удалось сохранить настройки: ' + str(e), parent=self.root)
        self.closing = True
        self.stop_session()


def main(role='local'):
    managed=ManagedClient.from_environment()
    os.chdir(managed.directory if managed else Path(sys.executable if getattr(sys, 'frozen', False) else __file__).resolve().parent)
    try:
        ctypes = __import__('ctypes')
        ctypes.windll.shcore.SetProcessDpiAwareness(2)
    except (OSError, AttributeError):
        pass
    root = tk.Tk()
    app=ScreenWindow(root, role, initial=managed.initial if managed else None,controlled=bool(managed))
    if managed:
        app.role_combo.configure(state='disabled')
        def command(name,message):
            if name=='shutdown': app.close()
            elif name=='apply':
                active=bool(app.session and not app.session.stop.is_set())
                data=validate_settings('screen',message['settings'],role,require_sources=message.get('start',True) or active)
                changed={key for key,value in data.items() if key in app.vars and app.vars[key].get()!=value}
                for key,value in data.items():
                    if key in app.vars and app.vars[key].get()!=value: app.vars[key].set(value)
                app.role_var.set(ROLES[role])
                live=set(LIVE_KEYS)|{'preview','fit','zoom','brightness','contrast','saturation','gamma','sharpness','mirror','rotation','topmost'}
                if (message.get('start',True) and not active) or (active and not changed<=live): app.apply()
            elif name=='settings':
                if not app.panel_visible: app.toggle_panel()
                root.deiconify(); root.lift()
            elif name=='snapshot': app.shot()
            elif name=='fullscreen': app.fullscreen()
            elif name=='region': app.select_region()
            elif name=='devices': app.refresh_devices()
        def state():
            devices={key:list(combo.cget('values')) for key,combo in app.device_combos.items()}
            active=bool(app.session and app.session.running() and not app.session.stop.is_set())
            return dict(active=active,role=role,status=app.video_status.get(),audio_status=app.audio_status.get(),
                        settings=app.snapshot(),devices=devices)
        managed.attach(root,command,state)
        app.toggle_panel()
        if managed.autostart: root.after(100,app.apply)
    root.mainloop()
    if managed: managed.close()
