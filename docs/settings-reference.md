# Полный каталог параметров Go

Сгенерировано из [settings_schema.json](../go-version/settings_schema.json) и
карты потребителей [generate_settings_reference.py](../tools/generate_settings_reference.py).
Обновление: `python tools/generate_settings_reference.py`; проверка: добавить `--check`.

Тип default сохранён в JSON-нотации: `"30"` — строка, `30` — число, `true` — bool.
Все строки Apply означают действие **после общей кнопки**, а не немедленно при вводе.
Клиентская колонка отражает schema.Client; аппаратная группа дополнительно скрыта в Go UI.

## Общие и миграционные значения

| Поле | Значение | Кто использует |
|---|---|---|
| Config.Role | client/server/local | Application, отдельный профиль роли |
| Config.Host | Пусто: broadcast; иначе IP/имя | Все receiver workers; смена требует restart |
| camera.host / screen.host | Пустая строка по умолчанию | Импорт Python; fallback для общего Host |

Camera и Screen — разные namespace: `camera.top` означает bool «поверх окон»,
`screen.top` — Y области, `screen.topmost` — флаг верхнего окна. Audio хранится в Screen.

## Камера

| Ключ | Назначение | Default | Допустимые значения / UI | На client | Потребитель | Применение |
|---|---|---|---|---|---|---|
| `camera.adaptive_video` | Подстраивать видео под связь | `true` | checkbox | нет | VideoSender / capture loop | Live, передатчик; обратная связь STAT |
| `camera.adaptive_resolution` | Разрешать уменьшение разрешения | `false` | checkbox | нет | budgetJPEG / WIC.EncodeBGRA | Live, передатчик; при включённой адаптации и JPEG-кодировании |
| `camera.camera` | Индекс камеры | `"0"` | 0, 1, 2, 3, 4, 5, 6, 7, 8, 9 | нет | captureCamera | Restart камеры на источнике |
| `camera.resolution` | Разрешение | `"1280x720"` | 640x480, 800x600, 1280x720, 1920x1080, 2560x1440, 3840x2160 | нет | captureCamera | Restart камеры на источнике |
| `camera.fps` | FPS захвата | `"30"` | 1…120 | нет | captureCamera | Restart камеры на источнике |
| `camera.codec` | Формат камеры | `"MJPG"` | MJPG, YUY2 | нет | captureCamera | Restart камеры на источнике |
| `camera.quality` | Качество JPEG | `80` | 30…95 | нет | captureCamera → WIC.EncodeBGRA | Live только encode path; в MJPG скрыто |
| `camera.receive_fps` | Лимит FPS клиента (0 — без лимита) | `"0"` | 0, 15, 20, 25, 30, 60, 120 | да | decodeVideo / Viewer | Live, локальный preview; sender не меняется |
| `camera.decode_scale` | JPEG 1/N (1 — полное качество) | `"1"` | 1, 2, 4 | да | decodeVideo / Viewer | Live, локальный preview; sender не меняется |
| `camera.preview` | Показывать изображение | `true` | checkbox | да | decodeVideo / Viewer | Live, локальный preview; sender не меняется |
| `camera.autoexp` | Автоэкспозиция | `true` | checkbox | нет | applyCameraControls (best effort) | Скрыто в Go UI; драйвер может не поддерживать |
| `camera.exposure` | Экспозиция | `-6` | -13…-1 | нет | applyCameraControls (best effort) | Скрыто в Go UI; драйвер может не поддерживать |
| `camera.autofocus` | Автофокус | `true` | checkbox | нет | applyCameraControls (best effort) | Скрыто в Go UI; драйвер может не поддерживать |
| `camera.focus` | Фокус | `0` | 0…255 | нет | applyCameraControls (best effort) | Скрыто в Go UI; драйвер может не поддерживать |
| `camera.autowb` | Автобаланс белого | `true` | checkbox | нет | applyCameraControls (best effort) | Скрыто в Go UI; драйвер может не поддерживать |
| `camera.white` | Баланс белого, K | `4500` | 2000…7500 | нет | applyCameraControls (best effort) | Скрыто в Go UI; драйвер может не поддерживать |
| `camera.ratio` | Соотношение сторон | `"Оригинал"` | Оригинал, 4:3, 16:9, 16:10, 21:9, 1:1 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.fit` | Масштабирование | `"Вписать"` | Вписать, Заполнить, Растянуть | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.zoom` | Зум | `1` | 1…5 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.rotation` | Поворот | `"0"` | 0, 90, 180, 270 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.mirror` | Зеркально | `false` | checkbox | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.flip` | Перевернуть | `false` | checkbox | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.top` | Окно камеры поверх окон | `false` | checkbox | да | Viewer.ApplyWindowOptions | Live, topmost камеры |
| `camera.brightness` | Яркость | `0` | -100…100 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.contrast` | Контраст | `1` | 0.2…3 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.saturation` | Насыщенность | `1` | 0…2 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.gamma` | Гамма | `1` | 0.2…3 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `camera.sharpness` | Резкость | `0` | 0…2 | да | imageQuad / GPU.Paint | Live, только локальный вид |

## Экран

| Ключ | Назначение | Default | Допустимые значения / UI | На client | Потребитель | Применение |
|---|---|---|---|---|---|---|
| `screen.adaptive_video` | Подстраивать видео под связь | `true` | checkbox | нет | VideoSender / capture loop | Live, передатчик; обратная связь STAT |
| `screen.adaptive_resolution` | Разрешать уменьшение разрешения | `false` | checkbox | нет | budgetJPEG / WIC.EncodeBGRA | Live, передатчик; при включённой адаптации и JPEG-кодировании |
| `screen.capture_kind` | Что захватывать | `"Монитор"` | Монитор, Все мониторы, Область, Окно (видимая область) | нет | screenRect / captureScreen | Live, только источник; зависит от capture_kind |
| `screen.monitor` | Монитор | `"1: основной"` | Динамический список устройств/окон | нет | screenRect / captureScreen | Live, только источник; зависит от capture_kind |
| `screen.window` | Окно (видимая область) | `""` | Динамический список устройств/окон | нет | screenRect / captureScreen | Live, только источник; зависит от capture_kind |
| `screen.left` | Область: X | `"0"` | -32768…32768 | нет | screenRect / captureScreen | Live, только источник; зависит от capture_kind |
| `screen.top` | Область: Y | `"0"` | -32768…32768 | нет | screenRect / captureScreen | Live, только источник; зависит от capture_kind |
| `screen.width` | Ширина области | `"1280"` | 1…16384 | нет | screenRect / captureScreen | Live, только источник; зависит от capture_kind |
| `screen.height` | Высота области | `"720"` | 1…16384 | нет | screenRect / captureScreen | Live, только источник; зависит от capture_kind |
| `screen.cursor` | Показывать указатель мыши | `true` | checkbox | нет | captureScreen / WIC.EncodeBGRA | Live, только источник |
| `screen.pause_video` | Пауза экрана | `false` | checkbox | нет | captureScreen / WIC.EncodeBGRA | Live, только источник |
| `screen.video_enabled` | Включить экран | `true` | checkbox | да | Application.start / UDP workers | Restart screen/audio Session |
| `screen.fps` | FPS экрана | `"30"` | 1…120 | нет | captureScreen / WIC.EncodeBGRA | Live, только источник |
| `screen.max_width` | Максимальная ширина (0 — исходная) | `"1920"` | 0…7680 | нет | captureScreen / WIC.EncodeBGRA | Live, только источник |
| `screen.quality` | Качество JPEG | `80.0` | 30…95 | нет | captureScreen / WIC.EncodeBGRA | Live, только источник |
| `screen.receive_fps` | Лимит FPS клиента (0 — без лимита) | `"0"` | 0, 15, 20, 25, 30, 60, 120 | да | decodeVideo / Viewer | Live, локальный preview; sender не меняется |
| `screen.decode_scale` | JPEG 1/N (1 — полное качество) | `"1"` | 1, 2, 4 | да | decodeVideo / Viewer | Live, локальный preview; sender не меняется |
| `screen.preview` | Показывать изображение | `true` | checkbox | да | decodeVideo / Viewer | Live, локальный preview; sender не меняется |
| `screen.video_port` | UDP-порт экрана | `"5002"` | 1024…65535 | да | Application.start / UDP workers | Restart screen/audio Session |
| `screen.audio_port` | UDP-порт звука | `"5003"` | 1024…65535 | да | Application.start / UDP workers | Restart screen/audio Session |
| `screen.fit` | Масштабирование | `"Вписать"` | Вписать, Заполнить, Растянуть | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `screen.zoom` | Зум | `1.0` | 1…5 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `screen.rotation` | Поворот | `"0"` | 0, 90, 180, 270 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `screen.mirror` | Зеркально | `false` | checkbox | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `screen.topmost` | Окно экрана поверх окон | `false` | checkbox | да | Viewer.ApplyWindowOptions | Live, topmost экрана |
| `screen.brightness` | Яркость | `0.0` | -100…100 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `screen.contrast` | Контраст | `1.0` | 0.2…3 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `screen.saturation` | Насыщенность | `1.0` | 0…2 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `screen.gamma` | Гамма | `1.0` | 0.2…3 | да | imageQuad / GPU.Paint | Live, только локальный вид |
| `screen.sharpness` | Резкость | `0.0` | 0…2 | да | imageQuad / GPU.Paint | Live, только локальный вид |

## Звук

| Ключ | Назначение | Default | Допустимые значения / UI | На client | Потребитель | Применение |
|---|---|---|---|---|---|---|
| `screen.audio_enabled` | Включить звук | `true` | checkbox | да | Application.start / UDP workers | Restart screen/audio Session |
| `screen.system_enabled` | Системный звук | `true` | checkbox | нет | sendAudio / captureAudio | Restart Session; только источник |
| `screen.system_device` | Системный выход для записи | `"По умолчанию"` | Динамический список устройств/окон | нет | sendAudio / captureAudio | Restart Session; только источник |
| `screen.system_gain` | Громкость системы, % | `100.0` | 0…200 | нет | sendAudio mix | Live, только источник |
| `screen.mic_enabled` | Микрофон | `true` | checkbox | нет | sendAudio / captureAudio | Restart Session; только источник |
| `screen.mic_device` | Микрофон / вход | `"По умолчанию"` | Динамический список устройств/окон | нет | sendAudio / captureAudio | Restart Session; только источник |
| `screen.mic_gain` | Громкость микрофона, % | `100.0` | 0…300 | нет | sendAudio mix | Live, только источник |
| `screen.send_mute` | Отключить передаваемый звук | `false` | checkbox | нет | sendAudio mix | Live, только источник |
| `screen.sample_rate` | Частота, Гц | `"48000"` | 16000, 24000, 44100, 48000 | нет | sendAudio / captureAudio | Restart Session; только источник |
| `screen.channels` | Каналы (1 — моно, 2 — стерео) | `"2"` | 1, 2 | нет | sendAudio / captureAudio | Restart Session; только источник |
| `screen.block_ms` | Блок захвата / вывода, мс | `"10"` | 5, 10, 20 | да | capture/mix/playback tickers | Restart Session; на client — вывод |
| `screen.buffer_ms` | Буфер приёмника, мс | `"40"` | 10, 20, 40, 80, 120, 200 | да | playbackAudio | Restart Session; действует только client |
| `screen.output_device` | Динамики / наушники | `"По умолчанию"` | Динамический список устройств/окон | да | playbackAudio | Restart Session; действует только client |
| `screen.volume` | Громкость приёмника, % | `100.0` | 0…200 | да | playbackAudio | Live только client; на server/local playback отсутствует |
| `screen.receive_mute` | Отключить воспроизведение | `false` | checkbox | да | playbackAudio | Live только client; на server/local playback отсутствует |

## Практические оговорки

- Role filtering скрывает source fields на client. На server/local ряд playback-полей
  всё ещё присутствует в общей схеме, но воспроизведение там не запускается.
  Не следует обещать влияние этих параметров на sender.
- Camera quality не задаёт качество raw MJPG камеры. Для него значение выбирает драйвер.
- Передатчик VB2Link показывает расширенные поля адаптации для камеры и экрана.
  Изменение ширины скрыто при отключённой адаптации и для нативного MJPG камеры.
- Доступный режим камеры выбирается ближайший, а не гарантированно равный requested.
- `receive_fps=0` снимает локальный лимит; /2 и /4 меняют decode size и чёткость.
- Hardware поля сохраняются, но capability-dependent; скрытие группы не удаляет их из JSON.
- Block/buffer_ms — настройки алгоритма/endpoint, не гарантия общей задержки.
- Schema ranges не заменяют проверку фактической доступности источника и согласованности ABI.
- При расширении поля одновременно обновлять metadata, validator, consumer, restart matrix и docs.
