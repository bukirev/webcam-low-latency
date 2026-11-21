import sys
from network import Sender, Receiver
import cv2, tkinter as tk, threading, time, json, os
from tkinter import ttk
from PIL import Image, ImageTk
from pathlib import Path
os.chdir(Path(sys.executable if getattr(sys, 'frozen', False) else __file__).resolve().parent)
MODE=sys.argv[1] if len(sys.argv)>1 else 'local'
SETTINGS=Path('settings_'+MODE+'.json')
DEFAULT=dict(host='',quality=80,camera='0',resolution='1280x720',fps='30',codec='MJPG',ratio='Оригинал',fit='Вписать',zoom=1,brightness=0,contrast=1,saturation=1,sharpness=0,gamma=1,rotation='0',mirror=False,flip=False,top=False,autoexp=True,autofocus=True,autowb=True,exposure=-6,focus=0,white=4500)
try: saved=json.loads(SETTINGS.read_text(encoding='utf-8'))
except Exception: saved={}
root=tk.Tk(); root.title('Webcam Low Latency 3.0 — '+MODE+''); root.geometry(saved.get('geometry','1200x760'))
V={}
for k,v in DEFAULT.items():
    cls=tk.BooleanVar if isinstance(v,bool) else tk.DoubleVar if isinstance(v,(int,float)) else tk.StringVar
    V[k]=cls(value=saved.get(k,v))
body=ttk.Frame(root); body.pack(fill='both',expand=True)
view=tk.Label(body,bg='black'); view.pack(side='left',fill='both',expand=True)
panel=ttk.Frame(body,width=285); panel.pack(side='right',fill='y'); panel.pack_propagate(False)
canvas=tk.Canvas(panel,width=270,highlightthickness=0); scroll=ttk.Scrollbar(panel,command=canvas.yview); scroll.pack(side='right',fill='y'); canvas.pack(side='left',fill='both',expand=True); canvas.configure(yscrollcommand=scroll.set)
controls=ttk.Frame(canvas,padding=8); win=canvas.create_window(0,0,anchor='nw',window=controls); controls.bind('<Configure>',lambda e:canvas.configure(scrollregion=canvas.bbox('all'))); canvas.bind('<Configure>',lambda e:canvas.itemconfigure(win,width=e.width))
bar=ttk.Frame(root); bar.pack(fill='x'); status=tk.StringVar(value='Запуск камеры…'); ttk.Label(bar,textvariable=status).pack(side='left',padx=6)
lock=threading.Lock(); latest=None; stop=threading.Event(); config=None; info=''; actual=0.; hardware=None; displayed=None; full=False; hidden=False

def snapshot(): return {k:v.get() for k,v in V.items()}
def update_config(*args):
    global config
    config=snapshot()
    root.attributes('-topmost',config['top'])

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
    ttk.Label(controls,text=label).pack(anchor='w'); ttk.Combobox(controls,textvariable=V[k],values=values,state='readonly').pack(fill='x',pady=(0,5))
def check(label,k): ttk.Checkbutton(controls,text=label,variable=V[k]).pack(anchor='w')
def scale(label,k,a,b):
    row=ttk.Frame(controls); row.pack(fill='x'); ttk.Label(row,text=label).pack(side='left'); val=ttk.Label(row); val.pack(side='right'); V[k].trace_add('write',lambda *_:val.configure(text=f'{V[k].get():.2f}')); val.configure(text=f'{V[k].get():.2f}'); ttk.Scale(controls,from_=a,to=b,variable=V[k]).pack(fill='x')
if MODE=='client':
    ttk.Label(controls,text='IP передатчика (пусто — поиск)').pack(anchor='w')
    ttk.Entry(controls,textvariable=V['host']).pack(fill='x')
if MODE=='server': scale('Качество JPEG','quality',30,95)
combo('Камера (индекс)','camera',[str(i) for i in range(10)])
combo('Разрешение','resolution',['640x480','800x600','1280x720','1920x1080','2560x1440','3840x2160']); combo('FPS','fps',['15','25','30','50','60','120']); combo('Формат','codec',['MJPG','YUY2']); combo('Соотношение сторон','ratio',['Оригинал','4:3','16:9','16:10','21:9','1:1']); combo('Масштабирование','fit',['Вписать','Растянуть','Заполнить']); scale('Зум','zoom',1,5); combo('Поворот','rotation',['0','90','180','270']); check('Зеркально','mirror'); check('Перевернуть','flip'); check('Поверх окон','top')
ttk.Label(controls,text='Обработка изображения').pack(anchor='w',pady=(10,0))
for label,k,a,b in [('Яркость','brightness',-100,100),('Контраст','contrast',.2,3),('Насыщенность','saturation',0,2),('Резкость','sharpness',0,2),('Гамма','gamma',.2,3)]: scale(label,k,a,b)
ttk.Label(controls,text='Настройки камеры (если поддерживаются)').pack(anchor='w',pady=(10,0))
check('Автоэкспозиция','autoexp'); scale('Экспозиция','exposure',-13,-1); check('Автофокус','autofocus'); scale('Фокус','focus',0,255); check('Автобаланс белого','autowb'); scale('Баланс белого','white',2000,7500)
def reset():
    for k,v in DEFAULT.items(): V[k].set(v)
ttk.Button(controls,text='Сброс настроек',command=reset).pack(fill='x',pady=8)
for v in V.values(): v.trace_add('write',update_config)
update_config()
net=None
def get_frame():
    with lock: return latest
def put_frame(frame):
    global latest
    with lock: latest=frame
if MODE!='client': threading.Thread(target=worker,daemon=True).start()
if MODE=='server': net=Sender(stop,get_frame,lambda:config)
if MODE=='client': net=Receiver(stop,lambda:config,put_frame)
if net: threading.Thread(target=net.run,daemon=True).start()
def render():
    global displayed
    with lock: frame=latest
    c=config
    if frame is not None:
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
        f=cv2.resize(f,(dw,dh),interpolation=cv2.INTER_LINEAR)
        import numpy as np
        if c['brightness'] or c['contrast']!=1 or c['gamma']!=1:
            x=np.arange(256,dtype=np.float32); lut=np.clip(((x/255)**(1/c['gamma'])*255-128)*c['contrast']+128+c['brightness'],0,255).astype('uint8'); f=cv2.LUT(f,lut)
        if c['saturation']!=1:
            hsv=cv2.cvtColor(f,cv2.COLOR_BGR2HSV); hsv[:,:,1]=np.clip(hsv[:,:,1].astype(float)*c['saturation'],0,255).astype('uint8'); f=cv2.cvtColor(hsv,cv2.COLOR_HSV2BGR)
        if c['sharpness']>0: f=cv2.addWeighted(f,1+c['sharpness'],cv2.GaussianBlur(f,(0,0),1),-c['sharpness'],0)
        displayed=np.zeros((th,tw,3),dtype=np.uint8); y=(th-dh)//2; x=(tw-dw)//2; displayed[y:y+dh,x:x+dw]=f
        photo=ImageTk.PhotoImage(Image.fromarray(cv2.cvtColor(displayed,cv2.COLOR_BGR2RGB))); view.configure(image=photo,text=''); view.image=photo; status.set(f'{w0}×{h0} | '+(net.status if MODE=='client' else f'{actual:.1f} FPS'+info+(' | '+net.status if net else '')))
    else: view.configure(image='',text=info,fg='white'); status.set(net.status if net else info)
    root.after(10,render)
def toggle_panel():
    global hidden
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
    data=snapshot(); data['geometry']=root.geometry(); SETTINGS.write_text(json.dumps(data,ensure_ascii=False,indent=2),encoding='utf-8'); stop.set(); root.destroy()
for label,fn in [('Настройки',toggle_panel),('Полный экран',fullscreen),('Снимок',shot)]: ttk.Button(bar,text=label,command=fn).pack(side='right')
root.bind('<F11>',lambda e:fullscreen()); root.bind('<F2>',lambda e:toggle_panel()); root.bind('<F5>',lambda e:shot()); root.bind('<Escape>',lambda e: fullscreen() if full else close()); root.protocol('WM_DELETE_WINDOW',close); render(); root.mainloop()
