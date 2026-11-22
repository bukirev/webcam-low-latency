# capture_windows.go — камера Media Foundation

[Исходник](../../go-version/capture_windows.go) · [FFI](native-windows.md)

## Интерфейс

`cameraDevices() → []NamedDevice` перечисляет камеры. ID — строка с текущим индексом,
Name — friendly name, полученный из IMFActivate. Это не стабильный USB ID.
`captureCamera(ctx,store,publish,status)` работает до cancel/ошибки, публикует
независимые JPEG-срезы. Панель оборачивает его в retryMedia.

Worker вызывает `mfStart`: COM thread + MFStartup, а в конце освобождает native
объекты и MFShutdown. `mfDevices` возвращает отдельные ссылки IMFActivate;
массив указателей освобождается CoTaskMemFree, каждый элемент — Release.
ActivateObject даёт IMFMediaSource, далее создаётся SourceReader с video processing
и low-latency attributes. У Source есть Shutdown перед Release.

## Выбор формата

Перебираются до 500 native media types. Оценка учитывает расстояние до желаемых
width/height и FPS; несоответствие предпочтительному subtype получает большой штраф.
Это выбор **ближайшего** режима, а не обещание точного разрешения/FPS.
При MJPG предпочитается MJPG, при YUY2 — YUY2. В статус выводится фактический режим.

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
