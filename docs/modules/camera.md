# capture_windows.go — камера Media Foundation

[Исходник](../../go-version/capture_windows.go) · [FFI](native-windows.md)

## Интерфейс

`cameraDevices() → []NamedDevice` перечисляет камеры. Через IMFActivate каталог
получает имя и символическую ссылку Media Foundation, которая сохраняет выбор
при изменении порядка перечисления. Старые профили с индексом продолжают
открываться. Перенос устройства в другой USB-разъём может изменить ссылку.
Кнопка обновления заменяет список текущим результатом перечисления. Если
выбранная камера отключена, её ID остаётся в профиле, а в списке доступны
только перечисленные устройства. Повторное подключение восстанавливает выбор
по ID. Старый профиль с именем камеры не переключается на другое устройство
только из-за совпавшего индекса.
`captureCamera(ctx,store,publish,status)` работает до cancel/ошибки, публикует
независимые JPEG-срезы. Панель оборачивает его в retryMedia.

[Панель нескольких камер](../../go-version/multi_camera_windows.go) хранит
независимые настройки устройств, порты, имена, состояния и предпросмотр.
Основная камера использует UDP 5001. Дополнительные получают свободные порты
5004–5010. Каждый экземпляр использует собственный захват, очередь свежего
JPEG и контроллер адаптации. Камеры можно запускать и останавливать по отдельности.

Worker вызывает `mfStart` для закрепления COM-потока. Платформа Media Foundation
запускается для первого пользователя и завершается после освобождения объектов
последним пользователем. `mfDevices` возвращает отдельные ссылки IMFActivate;
массив указателей освобождается CoTaskMemFree, каждый элемент — Release.
ActivateObject даёт IMFMediaSource, далее создаётся SourceReader с video processing
и low-latency attributes. [Асинхронный Source Reader](../../go-version/camera_reader_windows.go)
получает один запрос кадра за раз. Отмена или 5 секунд без callback завершают
попытку захвата. При повторном открытии VB2Link ждёт освобождения устройства.
Media source явно закрывается после освобождения Source Reader. Повторные
попытки постепенно увеличивают паузу до 8 секунд с небольшим разбросом.
[Тест callback](../../go-version/camera_reader_windows_test.go) проверяет
обработку прерванного чтения, отмену и поздний ответ.

## Выбор формата

Перебираются до 500 native media types. Оценка учитывает расстояние до желаемых
width/height и FPS; несоответствие предпочтительному subtype получает большой штраф.
Это выбор **ближайшего** режима, а не обещание точного разрешения/FPS.
При MJPG предпочитается MJPG, при YUY2 — YUY2. В журнал выводятся запрошенная
частота и выбранный режим. При наличии диапазона драйвера VB2Link запрашивает
ближайший FPS в этом диапазоне. Если устройство работает быстрее заданного
значения, VB2Link пропускает лишние кадры перед блокировкой буфера. Частота
захвата по USB зависит от поддерживаемых драйвером режимов.

В MJPG путь: ReadSample → contiguous buffer → Lock → копия JPEG → Unlock → Release.
Ни WIC decode, ни повторного JPEG encode для сети нет. Поэтому slider JPEG quality
не меняет сырой JPEG камеры и скрыт в форме.

Если нужен иной путь, SourceReader запрашивается RGB32, затем WIC EncodeBGRA
преобразует его в JPEG с заданным quality. Это уже другой CPU/quality budget.
Эта ветка предполагает плотно упакованный RGB32 подходящего orientation/stride;
перед переносом на другой драйвер нужно проверить actual output type и stride.
Не следует представлять её универсальным адаптером любого MF sample layout.

## Слоты MF, используемые кодом

| Интерфейс | Слот / операция |
|---|---|
| IMFAttributes | GetUINT32 7, GetUINT64 8, GetGUID 10, GetAllocatedString 13, SetUINT32 21, SetGUID 24 |
| IMFActivate | ActivateObject 33 |
| IMFMediaSource | Shutdown 12 |
| IMFSourceReader | GetNativeMediaType 5, SetCurrentMediaType 7, ReadSample 9, Flush 10 |
| IMFSample | ConvertToContiguousBuffer 41 |
| IMFMediaBuffer | Lock 3, Unlock 4 |

ReadSample синхронный. Флаг end-of-stream прекращает worker и позволяет retry.
Cancel проверяется между чтениями; зависание драйвера внутри ReadSample отдельным
watchdog не прерывается. Restart может кратковременно пересечься с закрытием старого worker.

## Аппаратные регуляторы

`applyCameraControls` пытается QueryInterface IAMCameraControl / IAMVideoProcAmp,
задавая exposure/focus/white balance и auto flags. MF source может не предоставить
эти старые интерфейсы. Ошибки не превращаются в подтверждённые capabilities.
Поэтому аппаратная группа в Go GUI скрыта, хотя значения остаются в профиле.
Для полноценной поддержки нужен отдельный capability/range/result contract.

## Перенос и проверки

В другой проект переносится JPEG passthrough и ownership sample/buffer, но не
зависимость capture от глобального Config/schema/UI. Желательная граница:
typed CameraConfig → source → immutable EncodedFrame, отдельно Status/telemetry.

Self-test перечисляет физическую камеру и пытается получить JPEG. Он не доказывает
совместимость всех USB-драйверов, многокамерность, orientation YUY2 и устойчивость
к любому hot unplug. Эти сценарии нужно добавлять при расширении поддержки.
