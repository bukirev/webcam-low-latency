# protocol.go — сеть видео и контейнер свежего кадра

Включение IPv4 broadcast для Windows вынесено в [broadcast_windows.go](../../go-version/broadcast_windows.go).

[Исходник](../../go-version/protocol.go) · [Wire format](../protocols.md)

## Компоненты

| Символ | Вход → выход | Владелец |
|---|---|---|
| `Latest[T]` | Значения → последнее доступное | Неблокирующие producer/consumer |
| `Status` | Текст → snapshot текста | Mutex |
| `fragments` | JPEG → последовательные UDP datagrams | Вызывающая горутина |
| `Assembler` | Неупорядоченные части → полный JPEG | Одна receiver goroutine |
| `receiveVideo` | Context, host/port → Latest JPEG | Сетевой worker |
| `VideoSender.Run` | Latest JPEG + SUB → UDP | Worker отправки + reader SUB |

Latest хранит один элемент. Put пытается записать, при заполнении удаляет старое
и снова пытается записать, не блокируя capture. Контейнер не определяет тотальный
порядок между произвольным числом producers: в данном приложении поток кадров
производится одним соответствующим worker. Не используйте его как надёжную очередь команд.

Канал не закрывают для остановки: Put после Close вызвал бы panic. Завершение идёт
через context. Контейнер также не вызывает destructor вытесненного значения;
для pooled/native objects при переносе нужно определить отдельную release policy.
Здесь по Latest передаются обычные неизменяемые JPEG-срезы, ими управляет GC.

## Приём

Receiver открывает свободный UDP4 порт, включает broadcast и увеличивает receive buffer.
Подписка обновляется раз в секунду. Первый подходящий IP/Session закрепляется;
смена разрешается после отсутствия данных. Собирается не история, а новые JPEG.
Assembler имеет предел частичных кадров/частей и cleanup по времени.

Deadline 100 ms позволяет обновлять ping/timeout даже без media. Отдельная горутина
закрывает socket по cancel; done channel завершает её, если функция вернулась сама.
`receiveVideo` повторяет обычные ошибки через секунду. Fatal runtime/panic это не перехватывает.

`nil` в output означает очистку изображения. Признак preview выключен не останавливает
сетевой receiver: иначе очередь kernel socket могла бы удержать устаревшие данные.
FPS limiting выполняется после сети в decoder, а не sleep перед ReadFromUDP.

## Отправка

JPEG из capture отделён Latest от отправки. SUB reader кладёт lease подписчика в
sync.Map; sender читает и удаляет устаревшие записи. Session случаен на создание sender,
seq увеличивается для каждого взятого JPEG. Несколько clients обслуживаются последовательно.

`fragments` использует один stack packet buffer: send должен потребить его синхронно.
Deadline 20 ms ставится на отдельную отправку, не на весь кадр; это не гарантированный
frame deadline при очень большом JPEG и множестве клиентов. Ошибки фрагментации/записи
не превращаются в надёжную доставку, часть отбрасывается.

## Что переносится

Чистые `Latest`, `newer`, packet fragmentation и bounded assembler можно выделить
в пакет после параметризации magic/limits и определения владения байтами.
Socket code требует broadcast helper Windows и политики discovery/security.
Сохранение wire format полезно для постепенной миграции; смена codecs/header
требует новой версии формата и interop tests.

Проверки: fragmentation/reorder/wrap, malformed/expired parts, latest mailbox,
UDP loopback и реальная Python-реализация в обоих направлениях.
Нет полноценного fault-injection теста на любую потерю/дубликат/перегрузку сети.
