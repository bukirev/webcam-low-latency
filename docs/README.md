# Документация для разработчиков и агентов

Состояние реализации: **Go 5.0 preview 2**, базовый код `634d513`, 22 ноября 2026.
Комментарии и документация, добавленные позднее, объясняют эту реализацию.
Назначение материала — сопровождение приложения и перенос проверенных подходов
в другие проекты. Это также описание ограничений: отсутствие функции не следует
принимать за обещание, что она реализована в другом модуле.

## Как читать

1. [Архитектура](architecture.md): процессы, горутины, пути данных, владение.
2. [Опыт разработки](lessons-learned.md): производительность и реальные сбои.
3. [Передача опыта в другой проект](porting-guide.md): границы выделения компонентов.
4. Нужные модули из таблицы ниже, затем их исходники и тесты.
5. [Протоколы](protocols.md), [параметры](settings-reference.md) и
   [проверки/диагностика](testing-and-operations.md) перед любым изменением формата.

Новый агент начинает с [AGENTS.md](../AGENTS.md). Пользовательский запуск описан
в [Go README](../go-version/README.md); здесь разбирается устройство приложения.

## Каждый модуль Go

Все файлы сейчас входят в один `package main`. Слово «модуль» в этом руководстве
означает логическую область ответственности файла, а не независимый Go module.

| Область / исходник | Документация | Что изучать |
|---|---|---|
| [app_windows.go](../go-version/app_windows.go) | [Панель и сеансы](modules/application.md) | Draft/Apply, роли, запуск, UI thread |
| [settings.go](../go-version/settings.go) | [Конфигурация](modules/settings.md) | Схема, миграция, immutable snapshots |
| [settings_schema.json](../go-version/settings_schema.json) | [Все поля](settings-reference.md) | Defaults, диапазоны, видимость, потребители |
| [protocol.go](../go-version/protocol.go) | [Сеть и очереди](modules/protocol.md) | UDP, сборка JPEG, Latest, подписчики |
| [native_windows.go](../go-version/native_windows.go) | [COM, FFI, WIC](modules/native-windows.md) | ABI, pinning, Release, JPEG |
| [capture_windows.go](../go-version/capture_windows.go), камера | [Камера](modules/camera.md) | Media Foundation, выбор формата, passthrough |
| Тот же файл, экран | [Экран и callbacks](modules/screen-capture.md) | GDI, координаты, постоянные callbacks |
| [audio.go](../go-version/audio.go) | [Алгоритмы звука](modules/audio-core.md) | PCM16, ring, resampling, единицы измерения |
| [audio_windows.go](../go-version/audio_windows.go) | [WASAPI и передача звука](modules/audio-windows.md) | Два источника, mix, playback, буферизация |
| [render_windows.go](../go-version/render_windows.go) | [Просмотр и GPU](modules/render.md) | Direct3D, shader, WM_PAINT, snapshot |
| [region_windows.go](../go-version/region_windows.go) | [Выбор области](modules/region.md) | Снимок, мышь, DPI, координаты |
| [session.go](../go-version/session.go) | [Повторный запуск](modules/session.md) | Context и границы восстановления |
| [diagnostics_windows.go](../go-version/diagnostics_windows.go) | [Диагностика](modules/diagnostics.md) | Fatal stack, журналы, health |
| [selftest_windows.go](../go-version/selftest_windows.go) | [Аппаратный self-test](modules/selftest.md) | Проверки Windows API и чтение отчёта |
| [build.ps1](../go-version/build.ps1), manifest, go.mod/sum | [Сборка](modules/build.md) | Standalone EXE, ресурсы, лицензии, ZIP |
| Четыре файла `*_test.go` | [Карта тестов](modules/tests.md) | 13 автоматических проверок и их границы |
| [doc.go](../go-version/doc.go) | Этот раздел | Вход через `go doc`, без runtime-логики |

## Другие разделы

- [Вывод камеры, экрана и звука в OBS](modules/obs.md).

- [Python 4.3: каждый модуль и соответствие Go](legacy-python.md).
- [Контракт UDP на уровне байтов](protocols.md).
- [Каталог всех параметров](settings-reference.md).
- [Диагностика, тестирование, сборка и выпуск](testing-and-operations.md).
- [Первичные источники и закреплённые зависимости](sources.md).

## Поддержание документации

[tools/generate_settings_reference.py](../tools/generate_settings_reference.py) читает
JSON-схему без импорта приложения и обновляет каталог ключей/defaults/видимости.
Карта consumers/restart в инструменте редактируется вместе с кодом; `--check`
проверяет, что опубликованная таблица соответствует этим входам.
[tools/check_docs.py](../tools/check_docs.py) проверяет локальные Markdown-ссылки
и наличие ссылок на все Go/Python исходники, инструменты и test fixtures.
Он не запускает capture и не обращается в сеть; внешние URL/Markdown anchors
и смысл изложенного требуют отдельной проверки.

## Какие результаты подтверждены

На целевом клиенте Intel 5405U пользователь сообщил о снижении суммарного CPU
с более 30% до менее 9%, прежней задержке и лучшем субъективном качестве экрана.
Это наблюдение конкретной установки, а не воспроизводимый лабораторный benchmark.
Настройки обеих версий, сетевые условия и численная сквозная задержка не зафиксированы.
Нельзя приписывать весь выигрыш одному языку Go или обещать такой же результат на другом ПК.

Вылет сервера подтверждён стеком `too many callback functions` и отдельным
воспроизведением старого кода. Исправление проверено 10 000 нативных перечислений,
13 тестами и работой сервера более шести минут, включая 240 секунд приёма трёх потоков.
Это проверка устранения конкретной ошибки, не гарантия многосуточной безотказности.
