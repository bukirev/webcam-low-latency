# Документация для разработчиков и агентов

Документация актуализирована по **VB2Link 0.1.0-preview.3**, публичный код
`6c8d277`, ветка `obs-native`. [Журнал выпусков](release-log.md) закрепляет историю
и артефакты, [журнал проверок](verification-log.md) — результаты и границы тестов.

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

Приложение `go-version` использует `package main` и пакет `internal/brand`.
Сборка плагина переносит выбранные Go-файлы в отдельный C-shared пакет.

| Область / исходник | Документация | Что изучать |
|---|---|---|
| [app_windows.go](../go-version/app_windows.go) | [Панель и сеансы](modules/application.md) | Draft/Apply, роли, запуск, UI thread |
| [about_windows.go](../go-version/about_windows.go) | [Установка и обновления](webcamgo-updates.md) | Версия приложения и запуск единого обновлятора |
| [sender_panel_windows.go](../go-version/sender_panel_windows.go) | [Панель передатчика](modules/application.md#панель-передатчика-vb2link) | Компактная форма, условия видимости, предпросмотр |
| [settings.go](../go-version/settings.go) | [Конфигурация](modules/settings.md) | Схема, миграция, immutable snapshots |
| [settings_schema.json](../go-version/settings_schema.json) | [Все поля](settings-reference.md) | Defaults, диапазоны, видимость, потребители |
| [protocol.go](../go-version/protocol.go) | [Сеть и очереди](modules/protocol.md) | UDP, сборка JPEG, Latest, подписчики |
| [discovery.go](../go-version/discovery.go), [adaptive_video.go](../go-version/adaptive_video.go) | [Контракт UDP](protocols.md) | FIND/HERE, STAT, гистерезис |
| [brand_windows.go](../go-version/internal/brand/brand_windows.go) | [Ресурсы Windows](webcamgo-updates.md#ресурсы-windows) | Оформление Walk, ICO и заставка |
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
| Go `*_test.go` | [Карта тестов](modules/tests.md) | 20 проверок приложения и отдельные тесты сборок менеджера |
| [doc.go](../go-version/doc.go) | Этот раздел | Вход через `go doc`, без runtime-логики |

## Другие разделы

- [Прямые источники VB2Link для OBS](modules/vb2link-direct.md).
- [Исторический мост Teleport](modules/obs.md).
- [Единая установка, обновления и совместимость VB2Link](webcamgo-updates.md).

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

Вылет сервера Go preview 1 подтверждён стеком `too many callback functions` и
воспроизведением старого кода. При выпуске Go preview 2 исправление проверили
10 000 нативных перечислений, 13 тестами того выпуска и работой сервера более
шести минут, включая 240 секунд приёма трёх потоков. Текущие результаты VB2Link
сводятся в [журнале проверок](verification-log.md).
