"""A lightweight Tk control panel; media stay in independently managed processes."""
import os
from pathlib import Path
import sys
import tkinter as tk
from tkinter import messagebox, ttk

from media_settings import (APP_VERSION, ROLES, DEFAULTS, CAMERA_FIELDS, SCREEN_FIELDS, AUDIO_FIELDS,
                            load_settings, read_json, settings_path, validate_settings, write_json, visible_fields)
from panel_control import ManagedProcess


class Dashboard:
    def __init__(self, root, role=None):
        self.root = root
        self.preferences = read_json('settings_dashboard.json')
        self.role = role if role in ROLES else self.preferences.get('role', 'client')
        if self.role not in ROLES: self.role = 'client'
        self.role_var = tk.StringVar(root, value=ROLES[self.role])
        self.host = tk.StringVar(root)
        self.message = tk.StringVar(root, value='Настройте потоки и нажмите «Запустить выбранное».')
        self.variables = {}
        for kind,defaults in DEFAULTS.items():
            self.variables[kind]={}
            for key,default in defaults.items():
                if key=='host': continue
                cls=tk.BooleanVar if isinstance(default,bool) else tk.DoubleVar if isinstance(default,(int,float)) else tk.StringVar
                self.variables[kind][key]=cls(root,value=default)
        self.dirty = {'camera': False, 'screen': False}
        self.workers = {'camera': None, 'screen': None}
        self.awaiting = {'camera':False,'screen':False}
        self.selected = {kind: tk.BooleanVar(root, value=self.preferences.get(kind+'_selected', True)) for kind in DEFAULTS}
        self.status = {kind: tk.StringVar(root, value='Остановлено') for kind in DEFAULTS}
        self.buttons = {}
        self.device_combos = {}
        self.last_devices = None
        self.loading = True
        self.closing = False
        root.title('Webcam Low Latency '+APP_VERSION+' — единая панель')
        root.minsize(850, 580)
        geometry = self.preferences.get('geometry', f'1100x{min(850, max(600,root.winfo_screenheight()-100))}')
        try: root.geometry(geometry)
        except tk.TclError: root.geometry('1100x760')
        style = ttk.Style(root)
        style.configure('Heading.TLabel', font=('Segoe UI', 15, 'bold'))
        style.configure('Card.TLabelframe.Label', font=('Segoe UI', 11, 'bold'))
        top = ttk.Frame(root, padding=(12, 10))
        top.pack(fill='x')
        ttk.Label(top, text='Камера · Экран · Звук', style='Heading.TLabel').pack(side='left')
        ttk.Label(top, text='Единое управление  '+APP_VERSION, foreground='#647184').pack(side='right')
        connection = ttk.Frame(root, padding=(12, 0, 12, 8))
        connection.pack(fill='x')
        ttk.Label(connection, text='Режим').pack(side='left')
        role_combo = ttk.Combobox(connection, textvariable=self.role_var, state='readonly',
                                  values=list(ROLES.values()), width=23)
        role_combo.pack(side='left', padx=(6, 18))
        role_combo.bind('<<ComboboxSelected>>', self.change_role)
        ttk.Label(connection, text='IP передатчика для обоих потоков').pack(side='left')
        self.host_entry = ttk.Entry(connection, textvariable=self.host, width=25)
        self.host_entry.pack(side='left', padx=6, fill='x', expand=True)
        ttk.Label(connection, text='Пусто — автопоиск', foreground='#647184').pack(side='left')
        actions = ttk.Frame(root, padding=(12, 0, 12, 10))
        actions.pack(fill='x')
        self.start_all_button = ttk.Button(actions, text='▶ Запустить выбранное', command=self.start_selected)
        self.start_all_button.pack(side='left', padx=(0, 6))
        ttk.Button(actions, text='■ Остановить всё', command=self.stop_all).pack(side='left', padx=3)
        self.apply_button=ttk.Button(actions, text='Применить', command=self.apply_all)
        self.apply_button.pack(side='left', padx=3)
        ttk.Button(actions, text='Экономия CPU обоим', command=self.economy).pack(side='right')
        cards = ttk.Frame(root, padding=(12, 0, 12, 8))
        cards.pack(fill='x')
        cards.columnconfigure((0, 1), weight=1, uniform='streams')
        for index, (kind, title) in enumerate((('camera', 'Камера'), ('screen', 'Экран и звук'))):
            card = ttk.LabelFrame(cards, text=title, padding=8, style='Card.TLabelframe')
            card.grid(row=0, column=index, padx=(0, 5) if index == 0 else (5, 0), sticky='nsew')
            ttk.Checkbutton(card, text='Запускать вместе', variable=self.selected[kind]).pack(anchor='w')
            ttk.Label(card, textvariable=self.status[kind], wraplength=490).pack(anchor='w', fill='x', pady=5)
            row = ttk.Frame(card)
            row.pack(fill='x')
            start = ttk.Button(row, text='▶ Старт', command=lambda k=kind: self.start_component(k))
            start.pack(side='left', padx=(0, 3))
            ttk.Button(row, text='■ Стоп', command=lambda k=kind: self.stop_component(k)).pack(side='left', padx=3)
            ttk.Button(row, text='Окно просмотра', command=lambda k=kind: self.open_window(k)).pack(side='left', padx=3)
            self.buttons[kind] = start
            row = ttk.Frame(card)
            row.pack(fill='x', pady=(5, 0))
            for text, command in [('Полный экран', 'fullscreen'), ('Снимок', 'snapshot')]:
                ttk.Button(row, text=text, command=lambda k=kind,c=command: self.window_command(k,c)).pack(side='left', padx=(0, 4))

        self.notebook = ttk.Notebook(root)
        self.notebook.pack(fill='both', expand=True, padx=12)
        self.rebuild_tabs()
        footer = ttk.Frame(root, padding=(12, 8))
        footer.pack(fill='x')
        ttk.Label(footer, textvariable=self.message, wraplength=1060).pack(anchor='w')
        ttk.Label(footer, text='Настройки действуют после «Применить». Закрытие панели останавливает открытые ею окна.',
                  foreground='#647184').pack(anchor='w', pady=(4, 0))
        self.host.trace_add('write', lambda *_: self.mark_dirty('both'))
        for kind, variables in self.variables.items():
            for var in variables.values(): var.trace_add('write', lambda *_, k=kind: self.mark_dirty(k))
        self.load_role()
        root.protocol('WM_DELETE_WINDOW', self.close)
        root.after(250, self.poll)

    def add_tab(self, title, kind, fields):
        outer = ttk.Frame(self.notebook)
        self.notebook.add(outer, text=title)
        canvas = tk.Canvas(outer, highlightthickness=0)
        scroll = ttk.Scrollbar(outer, orient='vertical', command=canvas.yview)
        scroll.pack(side='right', fill='y')
        canvas.pack(side='left', fill='both', expand=True)
        canvas.configure(yscrollcommand=scroll.set)
        inner = ttk.Frame(canvas, padding=6)
        win = canvas.create_window(0, 0, window=inner, anchor='nw')
        canvas.bind('<Configure>', lambda e: canvas.itemconfigure(win, width=e.width))
        inner.bind('<Configure>', lambda e: canvas.configure(scrollregion=canvas.bbox('all')))
        inner.columnconfigure((0, 1), weight=1, uniform='settings')
        actions = ttk.Frame(inner)
        actions.grid(row=0, column=0, columnspan=2, sticky='ew', pady=(0, 8))
        if kind == 'screen':
            if self.role!='client' or title=='Звук':
                text='Обновить устройства вывода' if self.role=='client' else 'Обновить устройства / окна'
                ttk.Button(actions, text=text, command=self.refresh_devices).pack(side='left', padx=6)
            if title == 'Экран' and self.role!='client':
                ttk.Button(actions, text='Выбрать область мышью…', command=lambda: self.window_command('screen','region')).pack(side='left')
        groups = {}
        for group, key, label, choices in fields:
            if group not in groups:
                number = len(groups)
                container = ttk.LabelFrame(inner, text=group, padding=8)
                container.grid(row=1+number//2, column=number%2, sticky='new', padx=5, pady=5)
                groups[group] = container
            parent = groups[group]
            default = DEFAULTS[kind][key]
            var = self.variables[kind][key]
            if isinstance(default, bool):
                ttk.Checkbutton(parent, text=label, variable=var).pack(anchor='w', pady=3)
                continue
            row = ttk.Frame(parent)
            row.pack(fill='x', pady=(2, 5))
            ttk.Label(row, text=label).pack(anchor='w')
            if choices == 'devices':
                widget = ttk.Combobox(row, textvariable=var, values=[default] if default else [], state='readonly')
                self.device_combos[key] = widget
            elif isinstance(choices, tuple) and len(choices)==2 and isinstance(choices[0], (int,float)):
                increment = .1 if key in ('zoom','contrast','gamma','saturation','sharpness') else 1
                widget = ttk.Spinbox(row, textvariable=var, from_=choices[0], to=choices[1], increment=increment)
            elif choices:
                widget = ttk.Combobox(row, textvariable=var, values=choices, state='readonly')
            else:
                widget = ttk.Entry(row, textvariable=var)
            widget.pack(fill='x')
        def bind_wheel(widget):
            def wheel(event):
                canvas.yview_scroll(-int(event.delta/120), 'units')
                return 'break'
            widget.bind('<MouseWheel>', wheel)
            for child in widget.winfo_children(): bind_wheel(child)
        bind_wheel(inner)

    def rebuild_tabs(self):
        for tab in self.notebook.winfo_children(): tab.destroy()
        self.device_combos.clear()
        self.last_devices=None
        self.visible_keys={kind:set() for kind in DEFAULTS}
        for title,kind,fields in [('Камера','camera',CAMERA_FIELDS),('Экран','screen',SCREEN_FIELDS),('Звук','screen',AUDIO_FIELDS)]:
            fields=visible_fields(fields,kind,self.role)
            self.visible_keys[kind].update(field[1] for field in fields)
            self.add_tab(title,kind,fields)

    def mark_dirty(self, kind):
        if not self.loading:
            for key in self.dirty if kind=='both' else (kind,): self.dirty[key] = True
            self.message.set('Есть изменения. Нажмите «Применить», чтобы передать их в окна просмотра.')

    def load_role(self):
        self.loading = True
        try:
            data = {kind: load_settings(kind, self.role) for kind in DEFAULTS}
            for kind, values in data.items():
                for key, var in self.variables[kind].items(): var.set(values[key])
                self.dirty[kind] = False
            self.host.set(data['camera']['host'] or data['screen']['host'])
            self.host_entry.configure(state='normal' if self.role=='client' else 'disabled')
        finally:
            self.loading = False

    def collect(self, kind, require_sources=True):
        values = {key: var.get() for key,var in self.variables[kind].items()}
        values['host'] = self.host.get().strip()
        return validate_settings(kind, values, self.role, require_sources=require_sources)

    def save_profile(self, kind, config):
        path = settings_path(kind, self.role)
        data = read_json(path)  # Preserve the preview window's geometry.
        data.update(config)
        write_json(path, data)

    def ensure_worker(self, kind, config, start=False):
        worker = self.workers[kind]
        if worker and worker.running():
            if worker.closing or worker.role != self.role:
                raise ValueError('Дождитесь завершения предыдущего окна')
            if start and worker.state.get('active'):
                worker.command('show')
                return worker
            if start:
                worker.command('apply', settings=config, start=True)
                self.awaiting[kind]=True
                self.dirty[kind]=False
        else:
            self.save_profile(kind, config)
            worker = ManagedProcess(kind, self.role, config, autostart=start)
            self.workers[kind] = worker
            self.status[kind].set('Открывается окно…')
            self.dirty[kind]=False
            self.awaiting[kind]=False
        return worker

    def handle_error(self, error):
        self.message.set(str(error))
        messagebox.showerror('Проверьте настройки', str(error), parent=self.root)

    def start_component(self, kind):
        try:
            self.ensure_worker(kind, self.collect(kind), start=True)
            self.message.set('Запуск '+('камеры' if kind=='camera' else 'экрана и звука')+'…')
        except (OSError, ValueError, tk.TclError, RuntimeError) as e: self.handle_error(e)

    def start_selected(self):
        try:
            kinds = [kind for kind in DEFAULTS if self.selected[kind].get()]
            if not kinds: raise ValueError('Выберите камеру или экран и звук')
            configs = {kind:self.collect(kind) for kind in kinds}
            for kind in kinds: self.ensure_worker(kind, configs[kind], start=True)
            self.message.set('Запускаются выбранные потоки. Статусы появятся на карточках.')
        except (OSError, ValueError, tk.TclError, RuntimeError) as e: self.handle_error(e)

    def apply_component(self, kind):
        try:
            worker = self.workers[kind]
            active = bool(worker and worker.running() and worker.state.get('active'))
            config = self.collect(kind, require_sources=active)
            if worker and worker.running():
                if worker.closing: raise ValueError('Дождитесь остановки окна')
                worker.command('apply', settings=config, start=False)
                self.awaiting[kind]=True
            else:
                self.save_profile(kind, config)
            self.dirty[kind] = False
            self.message.set('Настройки применены / сохранены: '+('камера' if kind=='camera' else 'экран и звук'))
            return True
        except (OSError, ValueError, tk.TclError, RuntimeError) as e:
            self.handle_error(e)
            return False

    def apply_all(self):
        try:
            for kind in DEFAULTS:
                worker = self.workers[kind]
                self.collect(kind, require_sources=bool(worker and worker.running() and worker.state.get('active')))
        except (ValueError, tk.TclError) as e:
            self.handle_error(e)
            return
        for kind in DEFAULTS:
            if not self.apply_component(kind): return
        self.message.set('Параметры отправлены в работающие окна и сохранены для следующего запуска.')

    def stop_component(self, kind):
        worker = self.workers[kind]
        if worker and worker.running():
            worker.stop()
            self.status[kind].set('Останавливается…')

    def stop_all(self):
        for kind in DEFAULTS: self.stop_component(kind)

    def open_window(self, kind):
        try:
            worker = self.ensure_worker(kind, self.collect(kind, require_sources=False))
            worker.command('show')
            self.message.set('Окно открыто. Захват начнётся после «Старт».')
        except (OSError, ValueError, tk.TclError, RuntimeError) as e: self.handle_error(e)

    def window_command(self, kind, command):
        try:
            worker = self.ensure_worker(kind, self.collect(kind, require_sources=False))
            if command in ('fullscreen','region'): worker.command('show')
            worker.command(command)
        except (OSError, ValueError, tk.TclError, RuntimeError) as e: self.handle_error(e)

    def refresh_devices(self):
        self.window_command('screen', 'devices')
        self.message.set('Список устройств обновляется в окне экрана; запись при этом не включается.')

    def economy(self):
        for kind in DEFAULTS:
            self.variables[kind]['receive_fps'].set('20')
            self.variables[kind]['decode_scale'].set('2')
        self.message.set('Выбрана экономия CPU для обоих потоков. Нажмите «Применить».')

    def change_role(self, *_):
        new_role = next(key for key,value in ROLES.items() if value==self.role_var.get())
        if new_role == self.role: return
        try:
            for kind in DEFAULTS: self.save_profile(kind, self.collect(kind, require_sources=False))
        except (OSError, ValueError, tk.TclError) as e:
            self.role_var.set(ROLES[self.role])
            self.handle_error(e)
            return
        self.stop_all()
        self.role = new_role
        self.load_role()
        self.rebuild_tabs()
        self.message.set('Режим изменён. Дождитесь остановки старых окон и запустите нужные потоки.')

    def update_from_worker(self, kind, worker):
        state = worker.state
        if state and worker.role == self.role and not self.dirty[kind] and not self.awaiting[kind]:
            values = state.get('settings', {})
            self.loading = True
            try:
                for key,var in self.variables[kind].items():
                    if key in values and var.get()!=values[key]: var.set(values[key])
                if 'host' in values and self.host.get()!=values['host']:
                    self.host.set(values['host'])
                    other='screen' if kind=='camera' else 'camera'
                    self.dirty[other]=True
                    self.message.set('IP изменён в окне просмотра. Примените настройки второго потока при необходимости.')
            finally:
                self.loading = False
        devices = state.get('devices')
        if kind=='screen' and devices and devices!=self.last_devices:
            self.last_devices = devices
            for key,widget in self.device_combos.items():
                choices = list(devices.get(key, []))
                if key.endswith('_device') and 'По умолчанию' not in choices: choices.insert(0,'По умолчанию')
                if choices: widget.configure(values=choices)

    def poll(self):
        for kind,worker in self.workers.items():
            if not worker: continue
            messages = worker.poll()
            if any(m.get('type')=='ack' and m.get('command')=='apply' for m in messages): self.awaiting[kind]=False
            if any(m.get('type')=='error' for m in messages):
                self.awaiting[kind]=False
                self.dirty[kind]=True
            if any(m.get('type')=='state' for m in messages): self.update_from_worker(kind, worker)
            running = worker.running()
            text = ('Останавливается…' if worker.closing else ('Работает' if worker.state.get('active') else 'Окно открыто, поток остановлен')) if running and worker.connected else ('Подключение к окну…' if running else 'Остановлено')
            if worker.error: text += ' | '+worker.error
            if running and worker.state:
                text += '\n'+worker.state.get('status','')
                if worker.state.get('audio_status'): text += '\n'+worker.state['audio_status']
            if self.status[kind].get()!=text: self.status[kind].set(text)
            self.buttons[kind].configure(state='disabled' if running and worker.closing or self.closing else 'normal')
        self.start_all_button.configure(state='disabled' if self.closing or any(w and w.running() and w.closing for w in self.workers.values()) else 'normal')
        if self.closing and not any(w and w.running() for w in self.workers.values()):
            self.finish_close()
            return
        self.root.after(250, self.poll)

    def close(self):
        if self.closing: return
        self.closing = True
        self.start_all_button.configure(state='disabled')
        self.message.set('Останавливаю окна и сохраняю настройки…')
        self.stop_all()

    def finish_close(self):
        try:
            for kind in DEFAULTS: self.save_profile(kind, self.collect(kind, require_sources=False))
            write_json('settings_dashboard.json', dict(role=self.role, geometry=self.root.geometry(),
                       **{kind+'_selected':var.get() for kind,var in self.selected.items()}))
        except (OSError, ValueError, tk.TclError) as e:
            messagebox.showerror('Сохранение настроек',str(e),parent=self.root)
        finally:
            self.root.destroy()


def main(role=None):
    os.chdir(Path(sys.executable if getattr(sys,'frozen',False) else __file__).resolve().parent)
    root = tk.Tk()
    Dashboard(root,role)
    root.mainloop()
