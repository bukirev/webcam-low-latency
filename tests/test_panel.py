import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import tkinter as tk
import unittest
from unittest.mock import patch

from dashboard import Dashboard
from media_settings import (DEFAULTS, CAMERA_FIELDS, SCREEN_FIELDS, AUDIO_FIELDS,
                            read_json, validate_settings, write_json)
from panel_control import JsonSocket, ManagedProcess

ROOT=Path(__file__).resolve().parents[1]


def wait_for(worker, condition, seconds=5):
    deadline=time.monotonic()+seconds
    while time.monotonic()<deadline:
        worker.poll()
        if condition(): return
        time.sleep(.02)
    raise AssertionError((worker.running(),worker.connected,worker.error,worker.state))


class ProtocolTests(unittest.TestCase):
    def test_json_handles_split_and_coalesced_unicode_messages(self):
        left,right=socket.socketpair()
        reader=JsonSocket(left)
        try:
            data=json.dumps({'text':'Экран и звук'},ensure_ascii=False).encode('utf-8')+b'\n'
            right.sendall(data[:4])
            self.assertIsNone(reader.receive(.1))
            right.sendall(data[4:]+b'{"number":2}\n')
            self.assertEqual(reader.receive(.1),{'text':'Экран и звук'})
            self.assertEqual(reader.receive(.1),{'number':2})
            right.sendall(b'[]\n')
            with self.assertRaises(ValueError): reader.receive(.1)
        finally:
            reader.close(); right.close()

    def test_authenticated_commands_and_graceful_stop_own_child_only(self):
        unrelated=subprocess.Popen([sys.executable,'-c','import time; time.sleep(30)'])
        with tempfile.TemporaryDirectory() as folder:
            command=[sys.executable,'-c',
                     'import time,runpy; time.sleep(.3); runpy.run_path('+repr(str(ROOT/'tests/control_worker.py'))+',run_name="__main__")']
            worker=ManagedProcess('camera','client',{'gamma':1},command=command,cwd=folder,autostart=False)
            try:
                bad=socket.create_connection(worker.listener.getsockname())
                bad.sendall(b'{"type":"hello","token":"wrong"}\n')
                bad.close()
                wait_for(worker,lambda:worker.connected and bool(worker.state))
                self.assertFalse(worker.state['active'])
                worker.command('apply',settings={'gamma':1.5},start=True)
                wait_for(worker,lambda:worker.state.get('settings',{}).get('gamma')==1.5)
                self.assertTrue(worker.state['active'])
                worker.stop()
                wait_for(worker,lambda:not worker.running())
                self.assertEqual(read_json(Path(folder)/'closed.json')['gamma'],1.5)
                self.assertIsNone(unrelated.poll())
            finally:
                if worker.running(): worker.process.terminate(); worker.process.wait(3)
                worker.stop_event.set()
                worker.thread.join(2)
                unrelated.terminate(); unrelated.wait(3)

    def test_panel_import_does_not_load_media_libraries(self):
        output=subprocess.check_output([sys.executable,'-c',
            'import dashboard,sys; print(any(k in sys.modules for k in ("cv2","numpy","pyaudiowpatch","mss")))'],cwd=ROOT)
        self.assertEqual(output.strip(),b'False')


class SettingsTests(unittest.TestCase):
    def test_panel_exposes_every_media_setting_except_shared_host(self):
        self.assertEqual({f[1] for f in CAMERA_FIELDS},set(DEFAULTS['camera'])-{'host'})
        self.assertEqual({f[1] for f in SCREEN_FIELDS+AUDIO_FIELDS},set(DEFAULTS['screen'])-{'host'})

    def test_inactive_settings_can_be_inspected_without_starting_sources(self):
        values=dict(DEFAULTS['screen'],video_enabled=False,audio_enabled=False,capture_kind='Окно (видимая область)',window='')
        validate_settings('screen',values,'server',require_sources=False)
        with self.assertRaises(ValueError): validate_settings('screen',values,'server')
        with self.assertRaises(ValueError): validate_settings('camera',dict(DEFAULTS['camera'],gamma=float('nan')),'client')

    def test_atomic_settings_preserve_unicode(self):
        with tempfile.TemporaryDirectory() as folder:
            target=Path(folder)/'settings.json'
            write_json(target,{'name':'Камера','geometry':'1200x760+100+100'})
            self.assertEqual(read_json(target)['name'],'Камера')
            self.assertEqual(len(list(Path(folder).iterdir())),1)


class FakeWorker:
    def __init__(self,kind,role,settings,autostart=True):
        self.role=role
        self.kind=kind
        self.autostart=autostart
        self.state={'active':autostart,'settings':dict(settings)}
        self.connected=True
        self.closing=False
        self.error=''
        self.alive=True
        self.sent=[]
        self.messages=[]
    def running(self): return self.alive
    def command(self,command,**payload): self.sent.append((command,payload)); return True
    def stop(self): self.closing=True; self.alive=False
    def poll(self):
        result,self.messages=self.messages,[]
        return result


class DashboardTests(unittest.TestCase):
    def setUp(self):
        self.folder=tempfile.TemporaryDirectory()
        self.previous=Path.cwd()
        os.chdir(self.folder.name)
        self.root=tk.Tk(); self.root.withdraw()
        self.root.after=lambda *args: None
        self.patcher=patch('dashboard.ManagedProcess',FakeWorker)
        self.patcher.start()
        self.app=Dashboard(self.root,'client')
    def tearDown(self):
        try: self.root.destroy()
        except tk.TclError: pass
        self.patcher.stop()
        os.chdir(self.previous)
        self.folder.cleanup()

    def test_open_settings_does_not_start_or_discard_pending_changes(self):
        self.app.open_window('camera')
        worker=self.app.workers['camera']
        self.assertFalse(worker.autostart)
        self.app.variables['camera']['gamma'].set(1.5)
        self.app.open_window('camera')
        self.assertTrue(self.app.dirty['camera'])
        self.app.apply_component('camera')
        self.assertEqual(worker.sent[-1][0],'apply')
        self.assertFalse(worker.sent[-1][1]['start'])
        self.app.update_from_worker('camera',worker)
        self.assertEqual(self.app.variables['camera']['gamma'].get(),1.5)

    def test_common_host_and_economy_apply_to_both_streams(self):
        self.app.host.set('192.0.2.8')
        self.app.start_selected()
        for worker in self.app.workers.values():
            self.assertTrue(worker.autostart)
            self.assertEqual(worker.state['settings']['host'],'192.0.2.8')
        self.app.economy()
        for worker in self.app.workers.values():
            command,payload=worker.sent[-1]
            self.assertEqual(command,'apply')
            self.assertEqual(payload['settings']['receive_fps'],'20')
            self.assertEqual(payload['settings']['decode_scale'],'2')

    def test_mode_change_stops_owned_windows_and_loads_separate_profile(self):
        self.app.start_selected()
        workers=list(self.app.workers.values())
        self.app.role_var.set('Передатчик')
        self.app.change_role()
        self.assertEqual(self.app.role,'server')
        self.assertTrue(all(w.closing for w in workers))
        self.assertTrue(Path('settings_client.json').exists())
        self.app.variables['camera']['fps'].set('15')
        self.app.apply_component('camera')
        self.assertEqual(read_json('settings_server.json')['fps'],'15')


if __name__=='__main__': unittest.main()
