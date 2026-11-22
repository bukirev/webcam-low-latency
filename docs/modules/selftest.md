# selftest_windows.go — аппаратная диагностика

[Исходник](../../go-version/selftest_windows.go)

`WebcamGo.exe --self-test` — явное действие диагностики, не обычный startup.
Создаёт report `go-self-test.json` рядом с EXE и по возможности пишет JSON в stdout.
GUI executable не всегда имеет видимую консоль, поэтому файл — основной результат.
Проверка кратко использует реальные camera/screen/microphone/output, но не сохраняет
их содержимое. Report содержит результаты и числа, не JPEG/PCM.

## Последовательность

| Шаг | Что действительно проверяется |
|---|---|
| Панель | Создание Application, forms client/server/local, validate draft, client whitelist |
| WIC | Синтетический JPEG → decode /2 с размером → encode → повторный decode |
| D3D | Draw на Viewer → readback system surface → проверка центрального цвета |
| MF | Перечисление камер; если есть, попытка JPEG 640x480 с timeout |
| Audio devices | Перечисление render/capture endpoints |
| GDI | Краткий capture/encode экрана с max_width=320 |
| WASAPI output | Открытие mix format без тестового тона |
| Mic/loopback | Примерно 1.1 секунды capture и число накопленных samples |
| Playback | Примерно 1.2 секунды muted output из нулевых samples |

`--gpu-test` отдельно создаёт минимальный Viewer и синтетическую BGRA-текстуру,
не запуская физические источники. Это короткий smoke D3D, не полная диагностика.

## Как читать результат

Ненулевой exit означает общую ошибку в критическом шаге. Однако ряд ветвей camera,
enumeration и audio пишет `*_error` и продолжает, а функция может вернуть nil.
**Exit 0 не означает, что каждое устройство успешно проверено**: нужно изучать JSON.
Пустой loopback при отсутствии воспроизведения допустим; не перепутать с гарантиями
настоящего system audio capture во время звука.

Проверка камеры использует timeout контекста, но синхронный драйверный ReadSample
не становится автоматически прерываемым. При отключённых разрешениях, занятом
устройстве и headless системе часть hardware checks ожидаемо недоступна.

Не запускать self-test бесконечным циклом как мониторинг: он создаёт окна/устройства.
Если приложение уже обслуживает camera, конкурирующее открытие может дать отказ.
Для долгой проверки используйте обычный Session и отдельный receiver.

## Перенос подхода

Синтетический CPU→codec→GPU→readback путь обнаруживает неправильный формат цвета,
ABI и отсутствие shader раньше пользовательского потока. Разделяйте deterministic
unit tests и environmental diagnostics; возвращайте structured per-check outcome
и не скрывайте warnings одним зелёным exit status. Не записывайте реальные медиа
в отчёт без отдельной необходимости и решения пользователя.
