"""Settings shared by the lightweight control panel and the media processes."""
import json
import math
import os
from pathlib import Path
import tempfile

APP_VERSION = '4.2'
ROLES = {'client': 'Приёмник', 'server': 'Передатчик', 'local': 'Локальный просмотр'}
CAMERA_DEFAULT = dict(host='', quality=80, camera='0', resolution='1280x720', fps='30', codec='MJPG',
                      ratio='Оригинал', fit='Вписать', zoom=1, brightness=0, contrast=1, saturation=1,
                      sharpness=0, gamma=1, rotation='0', mirror=False, flip=False, top=False,
                      autoexp=True, autofocus=True, autowb=True, exposure=-6, focus=0, white=4500,
                      receive_fps='0', decode_scale='1', preview=True)
SCREEN_DEFAULT = dict(host='', video_port='5002', audio_port='5003', video_enabled=True,
                      audio_enabled=True, capture_kind='Монитор', monitor='1: основной', window='',
                      left='0', top='0', width='1280', height='720', max_width='1920', fps='30',
                      quality=80., cursor=True, pause_video=False, preview=True,
                      system_enabled=True, mic_enabled=True, system_device='По умолчанию',
                      mic_device='По умолчанию', output_device='По умолчанию', system_gain=100.,
                      mic_gain=100., send_mute=False, receive_mute=False, volume=100.,
                      sample_rate='48000', channels='2', block_ms='10', buffer_ms='40',
                      fit='Вписать', zoom=1., brightness=0., contrast=1., saturation=1., gamma=1.,
                      sharpness=0., mirror=False, rotation='0', topmost=False, receive_fps='0', decode_scale='1')
DEFAULTS = {'camera': CAMERA_DEFAULT, 'screen': SCREEN_DEFAULT}
FPS_LIMITS = ('0', '15', '20', '25', '30', '60', '120')
FIT = ('Вписать', 'Заполнить', 'Растянуть')

# Fields: group, key, label, choices or numeric range. None means text input.
CAMERA_FIELDS = [
    ('Камера на передатчике', 'camera', 'Индекс камеры', tuple(map(str, range(10)))),
    ('Камера на передатчике', 'resolution', 'Разрешение', ('640x480','800x600','1280x720','1920x1080','2560x1440','3840x2160')),
    ('Камера на передатчике', 'fps', 'FPS захвата', (1,120)),
    ('Камера на передатчике', 'codec', 'Формат камеры', ('MJPG','YUY2')),
    ('Камера на передатчике', 'quality', 'Качество JPEG', (30,95)),
    ('Приём и нагрузка CPU', 'receive_fps', 'Лимит FPS клиента (0 — без лимита)', FPS_LIMITS),
    ('Приём и нагрузка CPU', 'decode_scale', 'JPEG 1/N (1 — полное качество)', ('1','2','4')),
    ('Приём и нагрузка CPU', 'preview', 'Показывать изображение', None),
    ('Аппаратные настройки камеры', 'autoexp', 'Автоэкспозиция', None),
    ('Аппаратные настройки камеры', 'exposure', 'Экспозиция', (-13,-1)),
    ('Аппаратные настройки камеры', 'autofocus', 'Автофокус', None),
    ('Аппаратные настройки камеры', 'focus', 'Фокус', (0,255)),
    ('Аппаратные настройки камеры', 'autowb', 'Автобаланс белого', None),
    ('Аппаратные настройки камеры', 'white', 'Баланс белого, K', (2000,7500)),
    ('Вид камеры', 'ratio', 'Соотношение сторон', ('Оригинал','4:3','16:9','16:10','21:9','1:1')),
    ('Вид камеры', 'fit', 'Масштабирование', FIT),
    ('Вид камеры', 'zoom', 'Зум', (1,5)),
    ('Вид камеры', 'rotation', 'Поворот', ('0','90','180','270')),
    ('Вид камеры', 'mirror', 'Зеркально', None),
    ('Вид камеры', 'flip', 'Перевернуть', None),
    ('Вид камеры', 'top', 'Окно камеры поверх окон', None),
]
SCREEN_FIELDS = [
    ('Источник экрана', 'capture_kind', 'Что захватывать', ('Монитор','Все мониторы','Область','Окно (видимая область)')),
    ('Источник экрана', 'monitor', 'Монитор', 'devices'),
    ('Источник экрана', 'window', 'Окно (видимая область)', 'devices'),
    ('Источник экрана', 'left', 'Область: X', (-32768,32768)),
    ('Источник экрана', 'top', 'Область: Y', (-32768,32768)),
    ('Источник экрана', 'width', 'Ширина области', (1,16384)),
    ('Источник экрана', 'height', 'Высота области', (1,16384)),
    ('Источник экрана', 'cursor', 'Показывать указатель мыши', None),
    ('Источник экрана', 'pause_video', 'Пауза экрана', None),
    ('Передача экрана', 'video_enabled', 'Включить экран', None),
    ('Передача экрана', 'fps', 'FPS экрана', (1,120)),
    ('Передача экрана', 'max_width', 'Максимальная ширина (0 — исходная)', (0,7680)),
    ('Передача экрана', 'quality', 'Качество JPEG', (30,95)),
    ('Приём и нагрузка CPU', 'receive_fps', 'Лимит FPS клиента (0 — без лимита)', FPS_LIMITS),
    ('Приём и нагрузка CPU', 'decode_scale', 'JPEG 1/N (1 — полное качество)', ('1','2','4')),
    ('Приём и нагрузка CPU', 'preview', 'Показывать изображение', None),
    ('Сеть экрана и звука', 'video_port', 'UDP-порт экрана', (1024,65535)),
    ('Сеть экрана и звука', 'audio_port', 'UDP-порт звука', (1024,65535)),
    ('Вид экрана', 'fit', 'Масштабирование', FIT),
    ('Вид экрана', 'zoom', 'Зум', (1,5)),
    ('Вид экрана', 'rotation', 'Поворот', ('0','90','180','270')),
    ('Вид экрана', 'mirror', 'Зеркально', None),
    ('Вид экрана', 'topmost', 'Окно экрана поверх окон', None),
]
AUDIO_FIELDS = [
    ('Источники звука', 'audio_enabled', 'Включить звук', None),
    ('Источники звука', 'system_enabled', 'Системный звук', None),
    ('Источники звука', 'system_device', 'Системный выход для записи', 'devices'),
    ('Источники звука', 'system_gain', 'Громкость системы, %', (0,200)),
    ('Источники звука', 'mic_enabled', 'Микрофон', None),
    ('Источники звука', 'mic_device', 'Микрофон / вход', 'devices'),
    ('Источники звука', 'mic_gain', 'Громкость микрофона, %', (0,300)),
    ('Источники звука', 'send_mute', 'Отключить передаваемый звук', None),
    ('Формат и буфер', 'sample_rate', 'Частота, Гц', ('16000','24000','44100','48000')),
    ('Формат и буфер', 'channels', 'Каналы (1 — моно, 2 — стерео)', ('1','2')),
    ('Формат и буфер', 'block_ms', 'Блок захвата / вывода, мс', ('5','10','20')),
    ('Формат и буфер', 'buffer_ms', 'Буфер приёмника, мс', ('10','20','40','80','120','200')),
    ('Воспроизведение', 'output_device', 'Динамики / наушники', 'devices'),
    ('Воспроизведение', 'volume', 'Громкость приёмника, %', (0,200)),
    ('Воспроизведение', 'receive_mute', 'Отключить воспроизведение', None),
]
for fields, group in ((CAMERA_FIELDS, 'Обработка вида камеры'), (SCREEN_FIELDS, 'Обработка вида экрана')):
    for key, label, limits in [('brightness','Яркость',(-100,100)), ('contrast','Контраст',(.2,3)),
                               ('saturation','Насыщенность',(0,2)), ('gamma','Гамма',(.2,3)), ('sharpness','Резкость',(0,2))]:
        fields.append((group, key, label, limits))


def settings_path(kind, role):
    if kind not in DEFAULTS or role not in ROLES:
        raise ValueError('Неизвестный поток или режим')
    return Path(f'settings_{"screen_" if kind == "screen" else ""}{role}.json')


def read_json(path):
    try:
        value = Path(path).read_text(encoding='utf-8')
        value = json.loads(value)
        return value if isinstance(value, dict) else {}
    except (OSError, ValueError):
        return {}


def write_json(path, data):
    path = Path(path).resolve()
    temp = None
    try:
        with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=path.parent,
                                         prefix=path.name+'.', suffix='.tmp', delete=False) as handle:
            temp = Path(handle.name)
            json.dump(data, handle, ensure_ascii=False, indent=2, allow_nan=False)
        os.replace(temp, path)
    finally:
        if temp and temp.exists():
            temp.unlink()


def load_settings(kind, role):
    result = dict(DEFAULTS[kind])
    saved = read_json(settings_path(kind, role))
    for key, default in result.items():
        value = saved.get(key, default)
        if isinstance(default, bool):
            if isinstance(value, bool): result[key] = value
        elif isinstance(default, str):
            if isinstance(value, str): result[key] = value
        elif isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value):
            result[key] = value
    return result


def validate_settings(kind, values, role, *, require_sources=True):
    if role not in ROLES or kind not in DEFAULTS:
        raise ValueError('Неизвестный режим')
    result = dict(DEFAULTS[kind])
    for key in result:
        if key in values: result[key] = values[key]
    fields = CAMERA_FIELDS if kind == 'camera' else SCREEN_FIELDS + AUDIO_FIELDS
    for _, key, label, choices in fields:
        value = result[key]
        default = DEFAULTS[kind][key]
        if isinstance(default, bool):
            if not isinstance(value, bool): raise ValueError(label + ': нужно да / нет')
        elif isinstance(choices, tuple) and len(choices) == 2 and isinstance(choices[0], (int, float)):
            number = float(value)
            if not math.isfinite(number) or not choices[0] <= number <= choices[1]:
                raise ValueError(f'{label}: допустимо {choices[0]}–{choices[1]}')
            if isinstance(default, str):
                if not number.is_integer(): raise ValueError(label + ': нужно целое число')
                result[key] = str(int(number))
            else:
                result[key] = number
        elif isinstance(choices, tuple):
            if value not in choices: raise ValueError(label + ': выберите значение из списка')
        elif not isinstance(value, str):
            raise ValueError(label + ': нужно текстовое значение')
    result['host'] = str(result['host']).strip()
    if len(result['host']) > 253 or any(c.isspace() for c in result['host']):
        raise ValueError('IP / имя передатчика: неверное значение')
    if kind == 'screen':
        if result['video_port'] == result['audio_port'] or '5001' in (result['video_port'], result['audio_port']):
            raise ValueError('Экран и звук требуют разных портов, кроме порта камеры 5001')
        if require_sources and not result['video_enabled'] and not result['audio_enabled']:
            raise ValueError('Включите экран или звук')
        if role != 'client' and require_sources:
            if result['audio_enabled'] and not (result['mic_enabled'] or result['system_enabled']):
                raise ValueError('Выберите источник звука')
            if result['capture_kind'] == 'Окно (видимая область)' and not result['window']:
                raise ValueError('Выберите окно для захвата')
    return result
