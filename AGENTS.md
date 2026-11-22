# Codex Configuration

## Memory System
<!-- Memory hook - loads memory system at session start -->
<!-- See commands/mem.md for memory operations -->

## Навигация по проекту

Начать с [docs/README.md](docs/README.md), затем [архитектуры](docs/architecture.md)
и [практического опыта](docs/lessons-learned.md). Для переноса решений в другой
проект прочитать [porting-guide](docs/porting-guide.md) и ограничения нужного модуля.
Это описание проверенной реализации, не инструкция считать все компоненты готовыми библиотеками.

- Актуальная нативная реализация: `go-version`, один `package main`, Windows x64.
- Python в корне — отдельная версия 4.3, также reference для совместимости UDP.
- Карта каждого Go-файла и тестов: `docs/README.md`; Python: `docs/legacy-python.md`.
- Binary release и source docs version могут обновляться отдельно.

## Инварианты при изменениях

1. Учитывать текущий Git diff: `settings_*.json`, Go profiles, logs и snapshots
   могут принадлежать пользователю; их не включать в source commit/ZIP.
2. На приёмнике показывать только действующие параметры; Apply остаётся одной точкой
   публикации draft. Наличие поля означает, что должен существовать его consumer.
3. ConfigStore.Get snapshots не изменять; перед правкой клонировать maps.
4. Видео сохраняет свежесть, звук — последовательность в bounded FIFO/rings.
5. UI/D3D принадлежат UI OS thread. COM/native buffers имеют явный ownership и release.
6. Не создавать syscall.NewCallback в покадровых циклах: native entry points постоянны,
   request context удаляется отдельно после завершения соответствующего API.
7. Изменение UDP header/magic/ordering сопровождать interop tests и документацией wire format.
8. Не выдавать retryMedia за panic recovery/watchdog; не обещать A/V sync и WAN security,
   которых текущий transport не реализует.
9. Сохранять в комментариях причину/invariant и lifetime, не только пересказ операции.
   Не приписывать драйверу/кодеку гарантию, подтверждённую лишь на одном устройстве.

## Проверки

Из go-version: `go test -count=1 -v ./...`; локальный toolchain при отсутствии PATH:
`..\.tools\go\bin\go.exe`. Отмечать SKIP optional Python interop.
Hardware self-test описан отдельно: он кратко включает реальные устройства.
Для изменения только комментариев проверить неизменность токенов/AST, формат,
ссылки и сборку; работающий сервер ради документации не перезапускать.
Детали тестов и выпуска: [testing-and-operations](docs/testing-and-operations.md).
