"""Authenticated, loopback-only JSON control. Video/audio never pass through here."""
from collections import deque
import hmac
import json
import os
import queue
import secrets
import select
import socket
import subprocess
import sys
import threading
import time
from pathlib import Path

ENV_KEY = 'WEBCAM_PANEL_CONTROL'
MAX_MESSAGE = 256 * 1024


class JsonSocket:
    def __init__(self, sock):
        self.sock = sock
        self.buffer = bytearray()
        sock.settimeout(.3)

    def send(self, value):
        data = json.dumps(value, ensure_ascii=False, allow_nan=False).encode('utf-8') + b'\n'
        if len(data) > MAX_MESSAGE:
            raise ValueError('Слишком большое сообщение управления')
        self.sock.sendall(data)

    def receive(self, timeout=.1):
        if b'\n' not in self.buffer:
            if not select.select([self.sock], [], [], timeout)[0]:
                return None
            data = self.sock.recv(65536)
            if not data:
                raise ConnectionError('Соединение с окном закрыто')
            self.buffer.extend(data)
        if len(self.buffer) > MAX_MESSAGE and b'\n' not in self.buffer:
            raise ValueError('Слишком большое сообщение управления')
        if b'\n' not in self.buffer:
            return None
        line, _, rest = self.buffer.partition(b'\n')
        if len(line)>MAX_MESSAGE: raise ValueError('Слишком большое сообщение управления')
        self.buffer = bytearray(rest)
        message = json.loads(line.decode('utf-8'))
        if not isinstance(message, dict):
            raise ValueError('Неверное сообщение управления')
        return message

    def close(self):
        try:
            self.sock.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        self.sock.close()


def process_command(kind, role):
    if kind not in ('camera', 'screen') or role not in ('client', 'server', 'local'):
        raise ValueError('Неизвестный поток / режим')
    args = ([sys.executable] if getattr(sys, 'frozen', False)
            else [sys.executable, str(Path(__file__).with_name('camera.py').resolve())])
    return args + (['screen', role] if kind == 'screen' else [role])


def unmanaged_environment():
    env = os.environ.copy()
    env.pop(ENV_KEY, None)
    return env


class ManagedProcess:
    """Own exactly one child; never search for or terminate unrelated processes."""
    def __init__(self, kind, role, settings, *, autostart=True, command=None, cwd=None):
        self.kind, self.role = kind, role
        self.events = queue.Queue()
        self.commands = queue.Queue(maxsize=32)
        self.stop_event = threading.Event()
        self.connected = False
        self.closing = False
        self.close_deadline = None
        self.state = {}
        self.error = ''
        self.listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.listener.bind(('127.0.0.1', 0))
        self.listener.listen(2)
        self.listener.settimeout(.2)
        self.token = secrets.token_hex(32)
        env = unmanaged_environment()
        directory=Path(cwd or Path(sys.executable if getattr(sys,'frozen',False) else __file__).resolve().parent).resolve()
        env[ENV_KEY] = json.dumps(dict(port=self.listener.getsockname()[1], token=self.token,
                                      kind=kind, role=role, settings=settings,autostart=autostart,directory=str(directory)), ensure_ascii=False)
        try:
            self.process = subprocess.Popen(command or process_command(kind, role), env=env,
                                             cwd=directory)
        except Exception:
            self.listener.close()
            raise
        self.thread = threading.Thread(target=self._run, daemon=True)
        self.thread.start()

    def _run(self):
        channel = None
        try:
            while not self.stop_event.is_set() and self.process.poll() is None:
                try:
                    sock, address = self.listener.accept()
                except socket.timeout:
                    continue
                candidate = JsonSocket(sock)
                deadline = time.monotonic() + 2
                hello = None
                try:
                    while hello is None and time.monotonic() < deadline:
                        hello = candidate.receive(.1)
                    if (address[0] != '127.0.0.1' or not hello or hello.get('type') != 'hello'
                            or not hmac.compare_digest(str(hello.get('token', '')).encode('utf-8'), self.token.encode('ascii'))):
                        candidate.close()
                        continue
                except (OSError, ValueError):
                    candidate.close()
                    continue
                channel = candidate
                self.events.put({'type': 'connected'})
                break
            self.listener.close()
            if channel is None:
                return
            while not self.stop_event.is_set() and self.process.poll() is None:
                for _ in range(8):
                    try:
                        channel.send(self.commands.get_nowait())
                    except queue.Empty:
                        break
                message = channel.receive(.1)
                if message is not None:
                    self.events.put(message)
        except (OSError, ValueError, ConnectionError) as e:
            if not self.closing and not self.stop_event.is_set():
                self.events.put({'type': 'error', 'error': str(e)})
        finally:
            self.listener.close()
            if channel:
                channel.close()
            self.events.put({'type': 'disconnected'})

    def command(self, command, **payload):
        if not self.running():
            return False
        try:
            self.commands.put_nowait(dict(type='command', command=command, **payload))
        except queue.Full:
            raise RuntimeError('Окно не успевает принимать команды')
        return True

    def running(self):
        return self.process.poll() is None

    def stop(self):
        if not self.closing:
            self.closing = True
            self.close_deadline = time.monotonic() + 10
            self.command('shutdown')

    def poll(self):
        messages = []
        while True:
            try:
                message = self.events.get_nowait()
            except queue.Empty:
                break
            messages.append(message)
            kind = message.get('type')
            if kind == 'connected': self.connected = True
            elif kind == 'disconnected': self.connected = False
            elif kind == 'state': self.state = message
            elif kind == 'error': self.error = message.get('error', 'Ошибка управления')
        if self.closing and self.running() and time.monotonic() > self.close_deadline:
            self.process.terminate()
            self.error = 'Окно не ответило на остановку; его процесс завершён'
        if not self.running():
            self.stop_event.set()
        return messages


class ManagedClient:
    def __init__(self, environment):
        self.initial = environment['settings']
        self.kind, self.role = environment['kind'], environment['role']
        self.autostart=environment.get('autostart',True)
        self.directory=environment.get('directory',str(Path.cwd()))
        self.incoming = queue.Queue(maxsize=32)
        self.outgoing = queue.Queue(maxsize=16)
        self.latest_state = None
        self.stop_event = threading.Event()
        sock = socket.create_connection(('127.0.0.1', int(environment['port'])), timeout=3)
        self.channel = JsonSocket(sock)
        self.channel.send(dict(type='hello', token=environment['token']))
        self.thread = threading.Thread(target=self._run, daemon=True)
        self.thread.start()

    @classmethod
    def from_environment(cls):
        value = os.environ.pop(ENV_KEY, None)
        if not value:
            return None
        return cls(json.loads(value))

    def _run(self):
        last_state = None
        try:
            while not self.stop_event.is_set():
                for _ in range(4):
                    try:
                        self.channel.send(self.outgoing.get_nowait())
                    except queue.Empty:
                        break
                state = self.latest_state
                if state is not None and state != last_state:
                    self.channel.send(dict(type='state', **state))
                    last_state = state
                message = self.channel.receive(.1)
                if message and message.get('type') == 'command':
                    self.incoming.put_nowait(message)
        except (OSError, ValueError, ConnectionError, queue.Full):
            # A managed process cannot outlive its controlling panel unnoticed.
            try:
                self.incoming.put_nowait({'command': 'shutdown'})
            except queue.Full:
                pass
        finally:
            self.channel.close()

    def attach(self, root, on_command, get_state):
        self.root, self.on_command, self.get_state = root, on_command, get_state
        self.last_status = 0.
        root.after(100, self._tick)

    def _tick(self):
        if self.stop_event.is_set():
            return
        try:
            for _ in range(8):
                try:
                    message = self.incoming.get_nowait()
                except queue.Empty:
                    break
                command = message.get('command')
                if command == 'show':
                    self.root.deiconify()
                    self.root.lift()
                    self.root.focus_force()
                else:
                    self.on_command(command, message)
                self.outgoing.put_nowait(dict(type='ack', command=command))
                if command == 'shutdown':
                    return
            now = time.monotonic()
            if now - self.last_status >= .5:
                self.latest_state = self.get_state()
                self.last_status = now
            self.root.after(100, self._tick)
        except Exception as e:
            try:
                self.outgoing.put_nowait(dict(type='error', error=str(e)))
            except queue.Full:
                pass
            try:
                self.root.after(100, self._tick)
            except Exception:
                pass

    def close(self):
        self.stop_event.set()
        self.channel.close()
