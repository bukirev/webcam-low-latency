# Python 4.3: документация каждого модуля

Python остаётся рабочей реализацией, историей оптимизаций и interop reference.
Go не импортирует эти модули в runtime. Общие поля schema и media formats не означают,
что backend захвата, процессы, lifetime и все UI effects идентичны.

## camera.py

[Исходник](../camera.py). Entry dispatcher: запуск без аргументов/`panel` открывает
Dashboard, `screen` передаёт управление screen_window, роли запускают camera window.
Файл выполняет top-level создание Tk, поэтому его нельзя бездумно импортировать
для анализа. Тесты отдельных функций используют AST/controlled namespace.

`worker` открывает DirectShow через cv2.VideoCapture, применяет codec/resolution/FPS
и hardware props при их изменении, публикует последний BGR под lock. Неудачное
чтение закрывает capture и повторяет подключение. Tk variables читаются на UI,
worker работает с сохранённым config snapshot, а не `.get()` Tk из фонового потока.

`render` использует identity кадра и ключ обработки/размера: повторный poll не должен
повторять все OpenCV filters/upload. Receiver использует один OpenCV processing thread,
чтобы два окна меньше конкурировали за CPU. FramePresenter выводит полученное изображение.
`panel_command` принимает команды управляемого окна, start_media и close управляют
worker lifecycle. Аналог в Go: Application + MF source + Viewer, в одном процессе.

Граница: Python network sender получает BGR и повторно делает JPEG encoding,
тогда как Go MJPG source может передать исходный JPEG без этого шага.

## dashboard.py

[Исходник](../dashboard.py). `Dashboard` — единая Tk-панель, роль, host, tabs и карточки.
`collect` собирает и валидирует draft, `apply_all` применяет обе компоненты в одной
точке, `ensure_worker` создаёт ManagedProcess при необходимости. Start уже активного
не обязан применять незавершённый draft. Economy выставляет поля для последующего Apply.

`poll/update_from_worker` забирают состояния дочерних процессов, `change_role`
останавливает старые и загружает другой профиль, `close/finish_close` завершают
только принадлежащие панели workers. Данные кадров через Dashboard не проходят.
Go переносит orchestration внутрь Application и ConfigStore, без JSON IPC между окнами.

Для расширения важно сохранить один Apply и role filtering: нельзя одновременно
оставить независимые активные контролы старого окна и панели без ясного владельца.

## media_settings.py

[Исходник](../media_settings.py). CAMERA_DEFAULT/SCREEN_DEFAULT и три списка FIELDS
определяют defaults, labels, numeric ranges/enums. CLIENT_KEYS задают видимость,
`visible_fields` фильтрует UI. `load_settings` проверяет совместимость сохранённых
типов, `validate_settings` нормализует значения, проверяет источники и конфликты портов.

`settings_path/read_json/write_json` отделяют persistence: JSON записывается через
temp и замену, defaults сохраняются при недоступном профиле. Видимость параметра
и обязанность worker его потребить — разные контракты.

Go settings_schema.json — отдельный snapshot этой схемы. Изменение Python файла
автоматически Go-схему не обновляет. Go validator также не полностью идентичен
Python validator по строгим типам/require_sources. Проверять обе реализации отдельно.

## panel_control.py

[Исходник](../panel_control.py). Это **локальный control plane**, не media transport.
`JsonSocket` передаёт UTF-8 JSON dictionary по строкам, ограничивая сообщение 256 KiB.
`ManagedProcess` создаёт localhost TCP listener на свободном порту, случайный token,
запускает единственного дочернего процесса и передаёт bootstrap через environment.
Handshake использует token; неподтверждённые соединения не получают управление.

`ManagedClient` читает bootstrap, поддерживает сеть в фоне и переносит commands
в Tk через очередь/after. Из фонового socket thread виджеты не вызываются.
`process_command` учитывает frozen EXE и запуск source, `unmanaged_environment`
убирает bootstrap при независимом запуске другого окна.

Подход переносится в process-isolated приложение, если нужны независимые native
workers. Не публиковать bootstrap/token, не заменять loopback listener открытым
LAN control API и не завершать процессы по общему имени вместо сохранённого Popen.

## network.py

[Исходник](../network.py). `send_frame` режет JPEG на WC30/WS40 parts 1300 bytes,
`Sender` хранит leases SUB и при новом BGR frame делает cv2.imencode.
`Receiver` продолжает draining UDP, собирает ограниченные partial frames, выбирает
новейший полный seq, учитывает source/session/timeout и decode visibility/FPS.
`decode_jpeg` выбирает OpenCV full/reduced-color /2,/4 mode.

Это фактическая reference implementation для двух Go interop tests.
Wire format совпадает, но внутренний decode/планирование и некоторые source-filter
детали отличаются; нельзя обещать идентичность всех failure behaviors.
Полный packet contract и отсутствие auth/encryption — в [protocols](protocols.md).

## screen_capture.py

[Исходник](../screen_capture.py). `monitor_list/window_list/capture_region`
разрешают source в физические desktop coordinates. `ScreenCapture.run` владеет
mss capture, получает BGRA, рисует cursor, преобразует в BGR, ограничивает max_width
и публикует новый frame. Период учитывает время работы. При паузе Python публикует
None, при ошибке сбрасывает frame и повторяет запуск.

`CursorPainter` содержит Win32/GDI ресурсы для курсора и hotspot, close освобождает их.
Callback перечисления окон здесь Python/ctypes с другим механизмом lifetime;
правила Go syscall.NewCallback нельзя механически приписывать ctypes.
Go использует прямой GDI DIB/WIC и при pause не публикует новый JPEG, поэтому
поведение последнего preview frame в pause различается.

## audio_stream.py

[Исходник](../audio_stream.py). WASAPI доступен через PyAudioWPatch/PortAudio.
`serialized_portaudio` и `audio_context` сериализуют опасные операции init/enumeration/
open/close; callbacks работают с PCMBuffer, а не управляют PortAudio lifecycle.
`list_devices/select_device` разрешают endpoints и loopback choices.

`CaptureSource` открывает источник и приводит формат, `AudioSender` смешивает
system/mic и отправляет WA40, `AudioReceiver` проверяет packets и управляет
`AudioPlayback`. `audio_packets/unpack_audio/newer/block_sizes` — wire/sequence
helpers. Stateful `Resampler` и `PCMBuffer` сохраняют длительность и ограничивают backlog.

Go заменил PortAudio нативным WASAPI, но сохранил PCM wire format. Interop test
проверяет настоящий Python pack/unpack. Звук нельзя отправлять через Latest mailbox,
потому что последовательно поступающие blocks несут разные участки сигнала.
Подробности единиц и ограничений: [audio-core](modules/audio-core.md).

## screen_window.py

[Исходник](../screen_window.py). `ScreenSession` объединяет capture/receiver и
audio workers, хранит применённый snapshot, stop event, frame и состояния.
`ScreenWindow` владеет Tk controls/draft, запускает сеанс только после Start/Apply
в нужном сценарии. В managed mode управление приходит от Dashboard.

`processed_preview` выполняет CPU rotation/zoom/цвет/resize; `render` кеширует
результат по кадру, параметрам и размеру, пропускает лишнюю работу при скрытом окне.
`wait_stopped` не блокирует Tk долгим ожиданием, а проверяет завершение с дедлайном.
`select_region` выбирает область на снимке; `shot/fullscreen/close` управляют окном.
Go распределяет это между Viewer, region dialog, source и Session context.

## video_display.py

[Исходник](../video_display.py). `FramePresenter` принадлежит Tk UI thread.
`show` конвертирует BGR→RGB/PIL и держит две PhotoImage на текущий размер.
Следующий кадр paste-ится в **непоказанную** PhotoImage, затем widget переключается.
Это избегает дорогого paste в видимую image и постоянных новых Tcl image handles.
Размер поменялся — storage создаётся заново. `clear` удаляет ссылки и placeholder.

Выигрыш ограничен toolkit: JPEG decode и обработка OpenCV остаются на CPU,
а image upload остаётся Tk/Pillow. Go D3D путь — иная архитектура, не та же
оптимизация, переведённая построчно на другой язык.

## Инструменты сборки и измерения

| Файл | Назначение и границы |
|---|---|
| [build_portable.ps1](../build_portable.ps1) | PyInstaller onedir frozen EXE, включает _internal; отличается от Go single EXE |
| [tools/package_release.py](../tools/package_release.py) | Собирает licenses, фильтрует profiles/logs, валидирует обязательные ZIP entries и CRC |
| [tools/benchmark_client.py](../tools/benchmark_client.py) | Синтетические BGR/JPEG, реальные Tk uploads; сравнивает render через Git v4.0.0 и актуальный код |
| START*.bat в корне | Запуск Python/frozen панели или отдельных окон; это не генерируемые BAT Go ZIP |
| [requirements.txt](../requirements.txt) | Python runtime dependencies; Go binary их не требует |

Benchmark измеряет process CPU time и ограниченный hidden-window путь, исключая
LAN delivery и видимую desktop composition. Его цифры нельзя приравнивать к процентам
Task Manager на другом CPU. Исторический исходник берётся из Git tag, поэтому
для воспроизведения нужен доступный tag v4.0.0.

## Python tests и fixtures

[tests/test_media.py](../tests/test_media.py) проверяет media algorithms/validation,
[tests/test_panel.py](../tests/test_panel.py) — schema/role/control plane,
[tests/test_client_performance.py](../tests/test_client_performance.py) — отказ
от повторной дорогой обработки без нового frame/изменения parameters.
[tests/control_worker.py](../tests/control_worker.py) — дочерняя test fixture,
не media worker: эмулирует apply/state/shutdown без Tk и аппаратного capture.
Эти 30 исторических Python tests не заменяют 13 Go tests и hardware self-test.
