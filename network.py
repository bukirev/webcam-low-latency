# Совместимый с Go UDP video protocol: заголовок 20 байт big-endian и JPEG-фрагменты.
# Свежий кадр важнее очереди старых: receiver продолжает draining и при лимите FPS.
# Wire format и ограничения: docs/protocols.md; перенос: docs/modules/protocol.md.
import socket, struct, time, threading, secrets
import cv2
import numpy as np
PORT=5000
DISC=5001
HEADER=struct.Struct('!4sQ I H H')
CHUNK=1300

def decode_jpeg(data, scale=1):
    flags={1:cv2.IMREAD_COLOR,2:cv2.IMREAD_REDUCED_COLOR_2,4:cv2.IMREAD_REDUCED_COLOR_4}
    return cv2.imdecode(np.frombuffer(data,np.uint8),flags.get(int(scale),cv2.IMREAD_COLOR))

# Пакетный формат совпадает с Go. JPEG уже готов; frame slicing не должно
# менять endian/header и границы частей 1300 bytes без новой версии протокола.
def send_frame(sock, addr, session, seq, data, magic=b'WC30'):
    n=(len(data)+CHUNK-1)//CHUNK
    if n>4096: return 0
    total=0
    for i in range(n):
        packet=HEADER.pack(magic,session,seq,i,n)+data[i*CHUNK:(i+1)*CHUNK]
        total+=sock.sendto(packet,addr)
    return total

# В Python producer даёт BGR frame; sender кодирует его JPEG один раз на новый
# объект кадра, затем рассылает всем подписчикам. Go raw MJPG path устроен иначе.
class Sender:
    def __init__(self, stop, get_frame, get_config, *, port=DISC, magic=b'WC30'):
        self.port=port; self.magic=magic
        self.stop=stop; self.get_frame=get_frame; self.get_config=get_config; self.status='Ожидание клиентов'; self.clients={}; self.session=secrets.randbits(64)
    def run(self):
        try:
            with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as control, socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as out:
                control.bind(('',self.port)); control.setblocking(False); out.settimeout(.03)
                last=None; seq=0; total=0; stamp=time.monotonic(); rate=0
                while not self.stop.is_set():
                    now=time.monotonic()
                    for _ in range(100):
                        try: msg,addr=control.recvfrom(256)
                        except BlockingIOError: break
                        if msg.startswith(self.magic+b' SUB '):
                            try: p=int(msg[9:]); assert 0<p<65536
                            except (ValueError,AssertionError): continue
                            self.clients[(addr[0],p)]=now
                    self.clients={a:t for a,t in self.clients.items() if now-t<5}
                    frame=self.get_frame(); c=self.get_config()
                    if self.clients and frame is not None and frame is not last:
                        last=frame; ok,jpg=cv2.imencode('.jpg',frame,[cv2.IMWRITE_JPEG_QUALITY,int(c['quality'])]); seq=(seq+1)&0xffffffff
                        if ok:
                            data=jpg.tobytes()
                            for addr in list(self.clients):
                                try: total+=send_frame(out,addr,self.session,seq,data,self.magic)
                                except OSError: pass
                    if now-stamp>=1: rate=total*8/(now-stamp)/1e6; total=0; stamp=now
                    self.status=f'UDP: {len(self.clients)} клиентов | {rate:.1f} Мбит/с'; self.stop.wait(.002)
        except OSError as e: self.status=f'Ошибка сети: {e}'

# Собирать неполные JPEG нужно ограниченно, а декодировать только свежий полный.
# Лимит FPS не должен останавливать draining socket и накапливать старые кадры.
class Receiver:
    def __init__(self,stop,get_config,on_frame, *, port=DISC, magic=b'WC30'):
        self.port=port; self.magic=magic
        self.stop=stop; self.get_config=get_config; self.on_frame=on_frame; self.status='Поиск передатчика…'
    def run(self):
        while not self.stop.is_set():
            try: self.receive()
            except OSError as e: self.status=f'Ошибка UDP: {e}'; self.stop.wait(1)
    def receive(self):
        with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as s:
            s.setsockopt(socket.SOL_SOCKET,socket.SO_BROADCAST,1); s.setsockopt(socket.SOL_SOCKET,socket.SO_RCVBUF,4*1024*1024); s.bind(('',0)); s.settimeout(.002 if self.magic==b'WS40' else .01)
            port=s.getsockname()[1]; ping=0; frames={}; source=None; last_seen=0; last_seq=None; last_config=None; allowed_ip=None; count=lost=total=0; stamp=time.monotonic(); next_decode=0.; previous_fps=None; self.on_frame(None)
            while not self.stop.is_set():
                now=time.monotonic(); host=self.get_config()['host'].strip() or '255.255.255.255'
                if host!=last_config:
                    source=None; frames.clear(); last_seq=None; ping=0; last_config=host; self.on_frame(None)
                    allowed_ip=socket.gethostbyname(host) if self.magic!=b'WC30' and host!='255.255.255.255' else None
                if now-ping>=1:
                    s.sendto(self.magic+f' SUB {port}'.encode(),(source[0] if source else host,self.port)); ping=now
                if source and now-last_seen>2: source=None; last_seq=None; frames.clear(); self.on_frame(None)
                newest=None
                batch_start=time.monotonic()
                for _ in range(2048):
                    if self.magic==b'WS40' and newest is not None and time.monotonic()-batch_start>.005: break
                    try: packet,addr=s.recvfrom(1500)
                    except socket.timeout: break
                    if len(packet)<=HEADER.size: continue
                    magic,session,seq,i,n=HEADER.unpack_from(packet)
                    if magic!=self.magic or not 0<n<=4096 or i>=n: continue
                    if allowed_ip and addr[0]!=allowed_ip: continue
                    key=(addr[0],session)
                    if source is not None and key!=source: continue
                    if source is None: source=key; last_seq=None; frames.clear()
                    last_seen=now; total+=len(packet)
                    if last_seq is not None and not 0<((seq-last_seq)&0xffffffff)<0x80000000: continue
                    if seq not in frames: frames[seq]=(now,n,{})
                    t,expected,parts=frames[seq]
                    if n!=expected: continue
                    parts[i]=packet[HEADER.size:]
                    if len(parts)==n:
                        data=b''.join(parts[j] for j in range(n)); del frames[seq]
                        if newest is None or 0<((seq-newest[0])&0xffffffff)<0x80000000: newest=(seq,data)
                    if len(frames)>8: del frames[next(iter(frames))]
                if newest:
                    c=self.get_config(); fps=float(c.get('receive_fps',0))
                    if fps!=previous_fps: next_decode=0.; previous_fps=fps
                    due=time.monotonic()
                    # Never sleep or queue compressed frames to enforce a limit.
                    # Keep draining UDP; a later iteration picks the newest frame.
                    if not c.get('_receive_visible',True) or (fps>0 and due+.002<next_decode):
                        frames={k:v for k,v in frames.items() if now-v[0]<.15}
                        newest=None
                if newest:
                    seq,data=newest; f=decode_jpeg(data,c.get('decode_scale',1))
                    if f is not None:
                        if fps>0: next_decode=due+1/fps if not next_decode else max(next_decode+1/fps,due)
                        if last_seq is not None: lost+=max(0,((seq-last_seq)&0xffffffff)-1)
                        last_seq=seq; count+=1; self.on_frame(f)
                frames={k:v for k,v in frames.items() if now-v[0]<.15}
                if now-stamp>=1:
                    elapsed=now-stamp; self.status=(f'{source[0]} | {count/elapsed:.1f} FPS | {total*8/elapsed/1e6:.1f} Мбит/с | пропуск кадров: {lost}' if source else 'Ожидание передатчика…'); count=lost=total=0; stamp=now
