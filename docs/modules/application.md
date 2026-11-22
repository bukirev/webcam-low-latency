# app_windows.go — панель, роли и сеансы

[Исходник](../../go-version/app_windows.go) · [Архитектура](../architecture.md)

## Ответственность

Собирает Walk MainWindow, три вкладки, карточки камеры и экрана/звука.
Хранит черновик в widgets, применённую конфигурацию в ConfigStore и работающие
сеансы в `map[string]*Session`. Не кодирует кадры и не микширует звук.

`main` закрепляет UI OS thread, выбирает cwd рядом с EXE, включает диагностику,
обрабатывает диагностические флаги, загружает роль и запускает message loop.
Роль по умолчанию `client`, допустимый первый аргумент `client/server/local`.
`--autostart` запускает оба сеанса с сохранёнными параметрами после создания панели.

## Основные функции

| Символ | Контракт |
|---|---|
| `Application.create` | Создать виджеты, перечислить устройства, построить forms, установить timer |
| `refreshDevices` | MF camera, render/capture endpoints, monitors, видимые окна |
| `forms` | Перестроить вкладки из schema с фильтром роли |
| `fieldWidget` | Из metadata получить checkbox, combo либо text input и getter |
| `draft` | Скопировать применённые maps и собрать UI values |
| `apply` | Validate → save → Store.Set → нужные restarts → rebuild forms |
| `economy` | Выставить draft receive_fps=20, decode_scale=2 обоим видам |
| `changeRole` | Остановить сеансы, загрузить профиль другой роли, перестроить UI |
| `start/stop/show` | Управлять контекстом и Viewer одного kind |
| `updateStatus` | Перенести mutex-protected Status в labels на UI thread |

## Draft и Apply

Поля не привязаны напрямую к worker maps. Повторный Start активного сеанса
вызывает только Show. Пресет и выбор области меняют draft и требуют Apply.
Apply сначала проверяет и сохраняет профиль; лишь после успеха публикует snapshot.
Падение/ошибка драйвера после этого не откатывает файл и остальные потоки.

Матрица restart в текущем коде:

| Изменение | Действие |
|---|---|
| Host | Restart активного сеанса |
| Camera/camera, resolution, fps, codec на источнике | Restart камеры |
| Screen ports, включение audio/video | Restart screen/audio Session |
| Sound sources/devices/output, rate, channels, block_ms, buffer_ms | Restart screen/audio Session |
| Screen capture kind/region/FPS/JPEG quality | Capture читает новые значения live |
| View adjustments / local preview limits | Читаются на decode/paint, без restart |
| Gains / mute / volume | Читаются live звуковыми циклами |

В приёмнике source fields скрыты по schema.Client. Дополнительно скрыта hardware
camera group, для MJPG скрыт quality. Это capability/UI решение, не удаление
сохранённых полей. Новый параметр должен иметь потребителя; одного поля в форме мало.

## Потоки и ресурсы

Application maps, widgets и session changes принадлежат UI. Session.active atomic
позволяет отличить закрытый Viewer, но true не гарантирует доставку/исправность источника.
Каждый start создаёт context, Viewer и decoder; server добавляет sender и capture,
client — receive; screen Session объединяет экран и звук под одним cancel.

Stop не ждёт завершения native ReadSample. Немедленный restart может пересечься
с освобождением старого устройства; retryMedia повторит открытие. При переносе
в сервис предпочтительно добавить явный join с дедлайном и состояниями stopping/failed.

Один постоянный TIMERPROC ищет панель в `panelTimers` по HWND. Dispose удаляет запись
и KillTimer, в том числе при self-test. Создавать новый callback на каждый tick нельзя.
При перестройке формы layout приостановлен: см. [разбор ошибки](../lessons-learned.md).

## Как расширять

Добавляя поле: обновить schema/defaults → найти consumer → выбрать live/restart →
добавить role visibility → документировать → проверить apply во время работы.
Добавляя поток: определить kind, Session lifecycle, собственную очередь и окна,
а не размещать долгую обработку внутри OnClicked/timer callback.

Проверки: аппаратный self-test форм всех трёх ролей; тест игнорирования source
параметров на клиенте; UI smoke изменения роли/Apply/Start/Stop. Автоматические
тесты не покрывают каждый пользовательский сценарий окна.
