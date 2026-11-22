"""Generate the documented schema inventory without importing either application.

The schema supplies keys/defaults/visibility/ranges; the small mapping below adds
implementation-specific consumers and Apply behavior. --check detects doc drift.
"""
import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCHEMA = ROOT / 'go-version' / 'settings_schema.json'
TARGET = ROOT / 'docs' / 'settings-reference.md'

VIEW = {'brightness', 'contrast', 'saturation', 'gamma', 'sharpness', 'fit',
        'zoom', 'rotation', 'mirror', 'flip', 'ratio'}
HARDWARE = {'autoexp', 'exposure', 'autofocus', 'focus', 'autowb', 'white'}


def consumer(kind, key):
    """Describe actual consumers, including controls dormant in some roles."""
    if key in VIEW:
        return 'imageQuad / GPU.Paint', 'Live, только локальный вид'
    if key in ('receive_fps', 'decode_scale', 'preview'):
        return 'decodeVideo / Viewer', 'Live, локальный preview; sender не меняется'
    if key == 'adaptive_video':
        return 'VideoSender / capture loop', 'Live, передатчик; обратная связь STAT'
    if key == 'adaptive_resolution':
        return 'budgetJPEG / WIC.EncodeBGRA', 'Live, передатчик; при включённой адаптации и JPEG-кодировании'
    if kind == 'camera':
        if key in HARDWARE:
            return 'applyCameraControls (best effort)', 'Скрыто в Go UI; драйвер может не поддерживать'
        if key == 'top':
            return 'Viewer.ApplyWindowOptions', 'Live, topmost камеры'
        if key == 'quality':
            return 'captureCamera → WIC.EncodeBGRA', 'Live только encode path; в MJPG скрыто'
        return 'captureCamera', 'Restart камеры на источнике'
    if key in ('capture_kind', 'monitor', 'window', 'left', 'top', 'width', 'height'):
        return 'screenRect / captureScreen', 'Live, только источник; зависит от capture_kind'
    if key in ('cursor', 'pause_video', 'fps', 'max_width', 'quality'):
        return 'captureScreen / WIC.EncodeBGRA', 'Live, только источник'
    if key == 'topmost':
        return 'Viewer.ApplyWindowOptions', 'Live, topmost экрана'
    if key in ('video_enabled', 'video_port', 'audio_port', 'audio_enabled'):
        return 'Application.start / UDP workers', 'Restart screen/audio Session'
    if key in ('system_gain', 'mic_gain', 'send_mute'):
        return 'sendAudio mix', 'Live, только источник'
    if key in ('volume', 'receive_mute'):
        return 'playbackAudio', 'Live только client; на server/local playback отсутствует'
    if key in ('output_device', 'buffer_ms'):
        return 'playbackAudio', 'Restart Session; действует только client'
    if key == 'block_ms':
        return 'capture/mix/playback tickers', 'Restart Session; на client — вывод'
    if key in ('sample_rate', 'channels', 'system_enabled', 'system_device', 'mic_enabled', 'mic_device'):
        return 'sendAudio / captureAudio', 'Restart Session; только источник'
    raise ValueError(f'Missing consumer description for {kind}.{key}')


def cell(value):
    return str(value).replace('|', r'\|').replace('\n', ' ')


def generate():
    schema = json.loads(SCHEMA.read_text(encoding='utf-8'))
    lines = [
        '# Полный каталог параметров Go', '',
        'Сгенерировано из [settings_schema.json](../go-version/settings_schema.json) и',
        'карты потребителей [generate_settings_reference.py](../tools/generate_settings_reference.py).',
        'Обновление: `python tools/generate_settings_reference.py`; проверка: добавить `--check`.', '',
        'Тип default сохранён в JSON-нотации: `"30"` — строка, `30` — число, `true` — bool.',
        'Все строки Apply означают действие **после общей кнопки**, а не немедленно при вводе.',
        'Клиентская колонка отражает schema.Client; аппаратная группа дополнительно скрыта в Go UI.', '',
        '## Общие и миграционные значения', '',
        '| Поле | Значение | Кто использует |',
        '|---|---|---|',
        '| Config.Role | client/server/local | Application, отдельный профиль роли |',
        '| Config.Host | Пусто: broadcast; иначе IP/имя | Все receiver workers; смена требует restart |',
        '| camera.host / screen.host | Пустая строка по умолчанию | Импорт Python; fallback для общего Host |', '',
        'Camera и Screen — разные namespace: `camera.top` означает bool «поверх окон»,',
        '`screen.top` — Y области, `screen.topmost` — флаг верхнего окна. Audio хранится в Screen.', '',
    ]
    seen = {'camera': set(), 'screen': set()}
    for section, title in [('camera', 'Камера'), ('screen', 'Экран'), ('audio', 'Звук')]:
        kind = 'camera' if section == 'camera' else 'screen'
        lines.extend([f'## {title}', '',
                      '| Ключ | Назначение | Default | Допустимые значения / UI | На client | Потребитель | Применение |',
                      '|---|---|---|---|---|---|---|'])
        for group, key, label, options in schema[section]:
            seen[kind].add(key)
            default = json.dumps(schema['defaults'][kind][key], ensure_ascii=False)
            if options == 'bool':
                allowed = 'checkbox'
            elif options is None:
                allowed = 'checkbox' if isinstance(schema['defaults'][kind][key], bool) else 'текст'
            elif options == 'devices':
                allowed = 'Динамический список устройств/окон'
            elif len(options) == 2 and isinstance(options[0], (int, float)):
                allowed = f'{options[0]}…{options[1]}'
            else:
                allowed = ', '.join(str(x) for x in options)
            target, apply = consumer(kind, key)
            visible = 'да' if key in schema['client'][kind] else 'нет'
            row = [f'`{kind}.{key}`', label, f'`{default}`', allowed, visible, target, apply]
            lines.append('| ' + ' | '.join(cell(x) for x in row) + ' |')
        lines.append('')
    for kind in ('camera', 'screen'):
        missing = set(schema['defaults'][kind]) - seen[kind] - {'host'}
        if missing:
            raise ValueError(f'Undocumented defaults: {kind}: {missing}')
    lines.extend([
        '## Практические оговорки', '',
        '- Role filtering скрывает source fields на client. На server/local ряд playback-полей',
        '  всё ещё присутствует в общей схеме, но воспроизведение там не запускается.',
        '  Не следует обещать влияние этих параметров на sender.',
        '- Camera quality не задаёт качество raw MJPG камеры. Для него значение выбирает драйвер.',
        '- Передатчик VB2Link показывает расширенные поля адаптации для камеры и экрана.',
        '  Изменение ширины скрыто при отключённой адаптации и для нативного MJPG камеры.',
        '- Доступный режим камеры выбирается ближайший, а не гарантированно равный requested.',
        '- `receive_fps=0` снимает локальный лимит; /2 и /4 меняют decode size и чёткость.',
        '- Hardware поля сохраняются, но capability-dependent; скрытие группы не удаляет их из JSON.',
        '- Block/buffer_ms — настройки алгоритма/endpoint, не гарантия общей задержки.',
        '- Schema ranges не заменяют проверку фактической доступности источника и согласованности ABI.',
        '- При расширении поля одновременно обновлять metadata, validator, consumer, restart matrix и docs.', '',
    ])
    return '\n'.join(lines)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    content = generate()
    if args.check:
        if not TARGET.exists() or TARGET.read_text(encoding='utf-8') != content:
            raise SystemExit('Settings documentation is out of date')
        print('Settings reference matches schema and consumer map')
    else:
        TARGET.write_text(content, encoding='utf-8', newline='\n')
        print(TARGET)
