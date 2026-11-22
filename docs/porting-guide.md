# Как использовать этот опыт в другом проекте

## Начать с контракта задачи

Уточните, нужен ли живой preview, полная запись, remote desktop, видеозвонок или
аудиопроизводство. В этом проекте приоритет — свежий кадр в доверенной LAN.
Отбрасывание старого видео подходит preview, но разрушит требования lossless recording.
Линейный resampler и best-effort PCM не заменят DSP/transport профессионального звука.

## Разделение на пакеты

Текущий `go-version` — один executable package. Для переноса не следует просто
импортировать `package main` или копировать все глобальные переменные.

| Выделяемый пакет | Кандидаты | Убрать из контракта |
|---|---|---|
| `framequeue` | Latest, ownership готового frame | HWND, UI config, скрытое уничтожение pooled objects |
| `wire` | newer, fragments, Assembler, PCM packets | Глобальный magic/ports без version contract |
| `audiobuffer` | FloatRing, Resampler | Неявные units и недопустимые channels/rates |
| `windows/comscope` | invoke/com/comThread, ownership utilities | Непроверенные signatures и предположение любой ABI |
| `windows/capture` | MF camera, GDI screen, enum registry | Values maps, UI labels, filesystem cwd |
| `windows/audio` | Endpoint adapter, capture/output | Глобальный store, legacy name parsing в DSP |
| `windows/presenter` | D3D texture/shader | Зависимость от конкретного MainWindow приложения |
| `supervision` | Context/retry/logging pattern | Общий fatal recovery без отдельного процесса |
| `app` | Draft/Apply, session orchestration | Покадровая обработка внутри UI events |

Чистый packet/audio core легче всего выделить первым. Windows adapters остаются
за platform build constraints и тестируются на целевых драйверах.

## Явные интерфейсы и ownership

Пример будущей границы, **не готовый API текущего приложения**:

```go
type EncodedFrame struct {
    Data []byte // immutable после Publish; producer больше не меняет backing array
}

type VideoSource interface {
    Run(ctx context.Context, publish func(EncodedFrame)) error
}

type ViewConfig struct {
    FPSLimit int
    DecodeScale int
}
```

Для PixelFrame дополнительно определяют ровно одного владельца и Release после
последнего чтения, или делают immutable copy с GC ownership. Это решение должно
быть одинаковым у очереди, decoder и presenter. Смешение этих двух подходов даёт
либо use-after-release, либо лишнее копирование и память.

Сохранённые параметры типизируют отдельно от UI и transport. Не переносите
`Values.Str` с `"<nil>"` fallback как валидатор внешнего API. У поля должны быть
unit, default, validation, consumer, роль, live/restart policy и тест.

## Порядок внедрения

1. Зафиксировать исходный CPU/latency/quality при известных FPS/resolution/codec.
2. Выделить и проверить пакетный формат на синтетических данных до аппаратного capture.
3. Реализовать постоянные native entry points и явный native resource lifetime.
4. Соединить source → bounded queue → sink; измерить backlog и память под перегрузкой.
5. Подключить decoder/presenter, пропуск decode при скрытом preview и лимите FPS.
6. Подключить звук с отдельными units/rings/resampler; не использовать видео mailbox.
7. Добавить UI draft/apply и supervision; статус не должен требовать чтения widgets worker.
8. Добавить crash evidence до первого release, протестировать fatal в дочернем процессе.
9. Проверить interop старого/нового transport и длительную работу на целевых устройствах.

## Что потребуется допроектировать

Для WAN: authenticated encrypted transport, congestion control, MTU/path discovery,
адаптивный bitrate и форматное версионирование. Для A/V sync: capture timestamps,
clock mapping, playout scheduling и допустимая разница задержек.
Для гарантированного uptime native драйвера: процессная изоляция, watchdog и восстановление.
Для аудиокачества: фильтрующий resampler, drift correction, echo cancellation и измерения.
Для enterprise deployment: installer/signing/update strategy и поддерживаемая матрица ОС.

Ни одна из этих возможностей не становится реализованной от переименования пакета.
Сначала нужен контракт и проверка, затем изменение implementation.

## Проверка нового проекта

Быстрый compile и 10 секунд preview недостаточны. Проверяйте долгий поток,
тысячи регистраций/вызовов, отключение устройств, resize/minimize, остановку при
ошибке и burst traffic. Для безопасности ресурса полезны не только heap metrics,
но и количество native handles, callbacks, goroutines и фактические размеры очередей.
Сохраняйте контрольный пример старой реализации и тест на причину регрессии.
