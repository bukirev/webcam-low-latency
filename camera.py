# Камера Python 4.3 и диспетчер запуска dashboard/screen. Код создаёт GUI при
# обычном запуске: для изучения используйте AST/исходник, не импорт ради introspection.
# Архитектура и контракты: docs/legacy-python.md, docs/modules/camera.md.
import sys
if len(sys.argv)==1 or sys.argv[1]=='panel':
    from dashboard import main
    main(sys.argv[2] if len(sys.argv)>2 else None)
    raise SystemExit
if len(sys.argv)>1 and sys.argv[1]=='screen':
    from screen_window import main
    main(sys.argv[2] if len(sys.argv)>2 else 'local')
    raise SystemExit
from network import Sender, Receiver
import cv2, tkinter as tk, threading, time, json, os
from tkinter import ttk
from PIL import Image, ImageTk
from video_display import FramePresenter
from pathlib import Path
from media_settings import CAMERA_DEFAULT, APP_VERSION, CLIENT_KEYS, validate_settings, write_json
from panel_control import ManagedClient, unmanaged_environment
managed=ManagedClient.from_environment()
os.chdir(managed.directory if managed else Path(sys.executable if getattr(sys, 'frozen', False) else __file__).resolve().parent)
MODE=sys.argv[1] if len(sys.argv)>1 else 'local'
SETTINGS=Path('settings_'+MODE+'.json')
DEFAULT=dict(CAMERA_DEFAULT)
DEFAULT.update(receive_fps='0',decode_scale='1',preview=True)
if MODE=='client': cv2.setNumThreads(1)
try: saved=json.loads(SETTINGS.read_text(encoding='utf-8'))
except Exception: saved={}
if managed: saved.update(validate_settings('camera',managed.initial,MODE))
root=tk.Tk(); root.title('VB2Link '+APP_VERSION+' — '+MODE); root.geometry(saved.get('geometry','1200x760'))
V={}
for k,v in DEFAULT.items():
    cls=tk.BooleanVar if isinstance(v,bool) else tk.DoubleVar if isinstance(v,(int,float)) else tk.StringVar
    V[k]=cls(value=saved.get(k,v))
body=ttk.Frame(root); body.pack(fill='both',expand=True)
view=tk.Label(body,bg='black'); view.pack(side='left',fill='both',expand=True)
presenter=FramePresenter(view)
panel=ttk.Frame(body,width=285); panel.pack(side='right',fill='y'); panel.pack_propagate(False)
canvas=tk.Canvas(panel,width=270,highlightthickness=0); scroll=ttk.Scrollbar(panel,command=canvas.yview); scroll.pack(side='right',fill='y'); canvas.pack(side='left',fill='both',expand=True); canvas.configure(yscrollcommand=scroll.set)
controls=ttk.Frame(canvas,padding=8); win=canvas.create_window(0,0,anchor='nw',window=controls); controls.bind('<Configure>',lambda e:canvas.configure(scrollregion=canvas.bbox('all'))); canvas.bind('<Configure>',lambda e:canvas.itemconfigure(win,width=e.width))
bar=ttk.Frame(root); bar.pack(fill='x'); status=tk.StringVar(value='Запуск камеры…'); ttk.Label(bar,textvariable=status).pack(side='left',padx=6)
lock=threading.Lock(); latest=None; stop=threading.Event(); config=None; info=''; actual=0.; hardware=None; displayed=None; full=False; hidden=False
last_render_frame=None; last_render_key=None; last_placeholder=None; last_status_time=0.; minimized=False

def snapshot(): return {k:v.get() for k,v in V.items()}
def update_config(*args):
    global config
    previous=config
    config=snapshot()
    if previous is None or previous['top']!=config['top']: root.attributes('-topmost',config['top'])

# Worker читает готовый config snapshot, а не Tk Variables из фонового потока.
# cap и его driver properties принадлежат этому worker; latest передаётся под lock.
def worker():
    global latest,info,actual
    cap=None; previous=None; applied=None; count=0; start=time.monotonic()
    while not stop.is_set():
        c=config
        if c is None: stop.wait(.05); continue
        key=tuple(c[k] for k in ('camera','resolution','fps','codec'))
        if key!=previous or cap is None:
            if cap is not None: cap.release()
            with lock: latest=None
            info='Подключение…'; cap=cv2.VideoCapture(int(c['camera']),cv2.CAP_DSHOW); previous=key; applied=None
            if not cap.isOpened():
                cap.release(); cap=None; info='Камера недоступна — повтор подключения'; stop.wait(1); continue
            w,h=map(int,c['resolution'].split('x')); cap.set(cv2.CAP_PROP_FOURCC,cv2.VideoWriter_fourcc(*c['codec'])); cap.set(cv2.CAP_PROP_FRAME_WIDTH,w); cap.set(cv2.CAP_PROP_FRAME_HEIGHT,h); cap.set(cv2.CAP_PROP_FPS,int(c['fps'])); cap.set(cv2.CAP_PROP_BUFFERSIZE,1)
            info=''; count=0; start=time.monotonic()
        props=[(cv2.CAP_PROP_AUTO_EXPOSURE,.75 if c['autoexp'] else .25),(cv2.CAP_PROP_AUTOFOCUS,int(c['autofocus'])),(cv2.CAP_PROP_AUTO_WB,int(c['autowb']))]
        if not c['autoexp']: props.append((cv2.CAP_PROP_EXPOSURE,c['exposure']))
        if not c['autofocus']: props.append((cv2.CAP_PROP_FOCUS,c['focus']))
        if not c['autowb']: props.append((cv2.CAP_PROP_WB_TEMPERATURE,c['white']))
        if props!=applied:
            failed=[str(p) for p,v in props if not cap.set(p,v)]; applied=props; info=' | Некоторые аппаратные настройки недоступны' if failed else ''
        ok,frame=cap.read()
        if not ok:
            cap.release(); cap=None
            with lock: latest=None
            stop.wait(.5); continue
        with lock: latest=frame
        count+=1; elapsed=time.monotonic()-start
        if elapsed>=1: actual=count/elapsed; count=0; start=time.monotonic()
    if cap is not None: cap.release()

def combo(label,k,values):
    if MODE=='client' and k not in CLIENT_KEYS['camera']: return
    ttk.Label(controls,text=label).pack(anchor='w'); ttk.Combobox(controls,textvariable=V[k],values=values,state='readonly').pack(fill='x',pady=(0,5))
def check(label,k):
    if MODE=='client' and k not in CLIENT_KEYS['camera']: return
    ttk.Checkbutton(controls,text=label,variable=V[k]).pack(anchor='w')
def scale(label,k,a,b):
    if MODE=='client' and k not in CLIENT_KEYS['camera']: return
    row=ttk.Frame(controls); row.pack(fill='x'); ttk.Label(row,text=label).pack(side='left'); val=ttk.Label(row); val.pack(side='right'); V[k].trace_add('write',lambda *_:val.configure(text=f'{V[k].get():.2f}')); val.configure(text=f'{V[k].get():.2f}'); ttk.Scale(controls,from_=a,to=b,variable=V[k]).pack(fill='x')
if MODE=='client':
    ttk.Label(controls,text='IP передатчика (пусто — поиск)').pack(anchor='w')
    ttk.Entry(controls,textvariable=V['host']).pack(fill='x')
    combo('Клиент: максимум FPS (0 — без лимита)','receive_fps',['0','15','20','25','30','60','120'])
    combo('Клиент: декодировать JPEG в масштабе 1/N','decode_scale',['1','2','4'])
    def economy():
        V['receive_fps'].set('20'); V['decode_scale'].set('2')
    ttk.Button(controls,text='Экономия CPU: 20 FPS, JPEG /2',command=economy).pack(fill='x',pady=5)
check('Показывать изображение','preview')
if MODE=='server': scale('Качество JPEG','quality',30,95)
combo('Камера (индекс)','camera',[str(i) for i in range(10)])
combo('Разрешение','resolution',['640x480','800x600','1280x720','1920x1080','2560x1440','3840x2160']); combo('FPS','fps',['15','25','30','50','60','120']); combo('Формат','codec',['MJPG','YUY2']); combo('Соотношение сторон','ratio',['Оригинал','4:3','16:9','16:10','21:9','1:1']); combo('Масштабирование','fit',['Вписать','Растянуть','Заполнить']); scale('Зум','zoom',1,5); combo('Поворот','rotation',['0','90','180','270']); check('Зеркально','mirror'); check('Перевернуть','flip'); check('Поверх окон','top')
ttk.Label(controls,text='Обработка изображения').pack(anchor='w',pady=(10,0))
for label,k,a,b in [('Яркость','brightness',-100,100),('Контраст','contrast',.2,3),('Насыщенность','saturation',0,2),('Резкость','sharpness',0,2),('Гамма','gamma',.2,3)]: scale(label,k,a,b)
if MODE!='client': ttk.Label(controls,text='Настройки камеры (если поддерживаются)').pack(anchor='w',pady=(10,0))
check('Автоэкспозиция','autoexp'); scale('Экспозиция','exposure',-13,-1); check('Автофокус','autofocus'); scale('Фокус','focus',0,255); check('Автобаланс белого','autowb'); scale('Баланс белого','white',2000,7500)
def reset():
    for k,v in DEFAULT.items():
        if MODE!='client' or k in CLIENT_KEYS['camera'] or k=='host': V[k].set(v)
ttk.Button(controls,text='Сброс настроек',command=reset).pack(fill='x',pady=8)
for v in V.values(): v.trace_add('write',update_config)
update_config()
net=None
def get_frame():
    with lock: return latest
def put_frame(frame):
    global latest
    with lock: latest=frame
if MODE=='server': net=Sender(stop,get_frame,lambda:config)
if MODE=='client': net=Receiver(stop,lambda:config,put_frame)
media_started=False
def start_media():
    global media_started
    if media_started: return
    media_started=True
    if MODE!='client': threading.Thread(target=worker,daemon=True).start()
    if net: threading.Thread(target=net.run,daemon=True).start()
if not managed or managed.autostart: start_media()
# UI poll не означает новый кадр. Ключ frame/filters/size позволяет пропустить
# повторную обработку и upload; изменение вида всё же должно инвалидировать кэш.
def render():
    global displayed,last_render_frame,last_render_key,last_placeholder,last_status_time,minimized,config
    with lock: frame=latest
    c=config
    now=time.monotonic()
    if now-last_status_time>=.2:
        minimized=root.state()=='iconic'
        text=(f'{frame.shape[1]}×{frame.shape[0]} | '+(net.status if MODE=='client' else f'{actual:.1f} FPS'+info+(' | '+net.status if net else ''))) if frame is not None else (net.status if net else info)
        if status.get()!=text: status.set(text)
        if not media_started: status.set('Остановлено — запуск из единой панели')
        last_status_time=now
    visible=c['preview'] and not minimized
    if c.get('_receive_visible',True)!=visible:
        config=dict(c,_receive_visible=visible); c=config
    if minimized:
        root.after(100,render); return
    if frame is not None and visible:
        size=view.winfo_width(),view.winfo_height()
        appearance=tuple(c[k] for k in ('rotation','mirror','flip','zoom','ratio','fit','brightness','contrast','gamma','saturation','sharpness'))
        key=(size,appearance)
        if frame is last_render_frame and key==last_render_key:
            root.after(10,render); return
        h0,w0=frame.shape[:2]; f=frame; rot=int(c['rotation'])
        if rot: f=cv2.rotate(f,{90:cv2.ROTATE_90_CLOCKWISE,180:cv2.ROTATE_180,270:cv2.ROTATE_90_COUNTERCLOCKWISE}[rot])
        if c['mirror']: f=cv2.flip(f,1)
        if c['flip']: f=cv2.flip(f,0)
        h,w=f.shape[:2]; z=c['zoom']; nw,nh=max(1,int(w/z)),max(1,int(h/z)); f=f[(h-nh)//2:(h-nh)//2+nh,(w-nw)//2:(w-nw)//2+nw]
        aw,ah=max(2,view.winfo_width()),max(2,view.winfo_height()); h,w=f.shape[:2]; ratio=w/h
        if c['ratio']!='Оригинал': a,b=map(int,c['ratio'].split(':')); ratio=a/b
        tw=min(aw,max(1,int(ah*ratio))); th=min(ah,max(1,int(tw/ratio)))
        if c['fit']=='Растянуть': dw,dh=tw,th
        else:
            if c['fit']=='Заполнить':
                if w/h>ratio: nw=max(1,int(h*ratio)); f=f[:,(w-nw)//2:(w-nw)//2+nw]
                else: nh=max(1,int(w/ratio)); f=f[(h-nh)//2:(h-nh)//2+nh,:]
            h,w=f.shape[:2]; s=min(tw/w,th/h); dw,dh=max(1,int(w*s)),max(1,int(h*s))
        if f.shape[1]!=dw or f.shape[0]!=dh: f=cv2.resize(f,(dw,dh),interpolation=cv2.INTER_LINEAR)
        import numpy as np
        if c['brightness'] or c['contrast']!=1 or c['gamma']!=1:
            x=np.arange(256,dtype=np.float32); lut=np.clip(((x/255)**(1/c['gamma'])*255-128)*c['contrast']+128+c['brightness'],0,255).astype('uint8'); f=cv2.LUT(f,lut)
        if c['saturation']!=1:
            hsv=cv2.cvtColor(f,cv2.COLOR_BGR2HSV); hsv[:,:,1]=np.clip(hsv[:,:,1].astype(float)*c['saturation'],0,255).astype('uint8'); f=cv2.cvtColor(hsv,cv2.COLOR_HSV2BGR)
        if c['sharpness']>0: f=cv2.addWeighted(f,1+c['sharpness'],cv2.GaussianBlur(f,(0,0),1),-c['sharpness'],0)
        if dh==th and dw==tw: displayed=f
        else:
            displayed=np.zeros((th,tw,3),dtype=np.uint8); y=(th-dh)//2; x=(tw-dw)//2; displayed[y:y+dh,x:x+dw]=f
        presenter.show(displayed)
        last_render_frame=frame; last_render_key=key; last_placeholder=None
    else:
        text=info if visible else 'Предпросмотр отключён'
        if text!=last_placeholder:
            presenter.clear(text); view.configure(fg='white'); last_placeholder=text
        displayed=None; last_render_frame=None; last_render_key=None
    root.after(10,render)
def toggle_panel():
    global hidden
    if managed and hidden: return
    hidden=not hidden
    if hidden: panel.pack_forget()
    else: panel.pack(side='right',fill='y',before=view)
def fullscreen():
    global full
    full=not full; root.attributes('-fullscreen',full)
def shot():
    if displayed is not None:
        p=Path('snapshots'); p.mkdir(exist_ok=True); target=p/(time.strftime('%Y%m%d_%H%M%S')+f'_{time.time_ns()%1000000}.png'); target.write_bytes(cv2.imencode('.png',displayed)[1].tobytes())
def close():
    try:
        data=snapshot(); data['geometry']=root.geometry(); write_json(SETTINGS,data)
    finally:
        stop.set(); root.destroy()
def open_screen():
    import subprocess
    command=[sys.executable] if getattr(sys,'frozen',False) else [sys.executable,str(Path(__file__).resolve())]
    subprocess.Popen(command+['screen',MODE],env=unmanaged_environment())
viewer_actions=[('Полный экран',fullscreen),('Снимок',shot)]
if not managed: viewer_actions=[('Экран и звук',open_screen),('Настройки',toggle_panel)]+viewer_actions
for label,fn in viewer_actions: ttk.Button(bar,text=label,command=fn).pack(side='right')
def panel_command(command,message):
    if command=='shutdown': close()
    elif command=='apply':
        data=validate_settings('camera',message['settings'],MODE)
        for key,value in data.items():
            if key in V and V[key].get()!=value: V[key].set(value)
        if message.get('start',True): start_media()
    elif command=='snapshot': shot()
    elif command=='fullscreen': fullscreen()
    elif command=='settings':
        if hidden: toggle_panel()
        root.deiconify(); root.lift()
if managed:
    managed.attach(root,panel_command,lambda:dict(active=media_started and not stop.is_set(),role=MODE,status=status.get(),settings=snapshot()))
    toggle_panel()
root.bind('<F11>',lambda e:fullscreen()); root.bind('<F2>',lambda e:toggle_panel()); root.bind('<F5>',lambda e:shot()); root.bind('<Escape>',lambda e: fullscreen() if full else close()); root.protocol('WM_DELETE_WINDOW',close); render(); root.mainloop()
if managed: managed.close()
