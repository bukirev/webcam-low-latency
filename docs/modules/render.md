# render_windows.go — декодирование, Viewer и Direct3D

[Исходник](../../go-version/render_windows.go) · [Ownership](../architecture.md)

## Decoder отделён от UI

Для каждого Viewer отдельный WIC COM worker читает Latest JPEG. Nil очищает frame.
До decode проверяются preview, Win32 visible/iconic и receive_fps. Сеть в это
время продолжает принимать и собирать новые кадры. decode_scale выбирает 1/2/4.
Декодированный PixelFrame передаётся Viewer.Publish; producer больше его не трогает.

FPS limiter не строит расписание старых JPEG и не sleep перед сетью: ранний кадр
пропускается, позже будет выбран свежий. Это лимит локальной обработки, не FPS sender.

## Viewer и кадры

Viewer хранит один frame под mutex, closed atomic и сохранённые HWND. Publish
освобождает старый buffer и вызывает InvalidateRect. Windows объединяет WM_PAINT:
на каждый принятый frame не создаётся отдельное замыкание UI, удерживающее его память.
Paint держит тот же mutex во время чтения Pixels и upload текстуры.

Walk widgets и D3D device используются UI OS thread. Worker проверяет Win32 HWND
и читает ConfigStore, но не вызывает свойства Walk. Close ставит closed, освобождает
frame, закрывает GPU и отменяет Session через onClose. Late Publish видит closed
и сразу возвращает свой buffer в pool.

## GPU

D3D9 HAL device windowed, swap effect DISCARD, Present immediate. Pixel shader 3.0
компилируется через d3dcompiler_47.dll один раз sync.Once, включая кеш ошибки.
SOFTWARE_VERTEXPROCESSING относится к четырём вершинам, а не переносу pixel shader на CPU.

Dynamic BGRA texture пересоздаётся при другом размере frame. LockRect даёт native
Pitch, поэтому копирование идёт построчно, без предположения Pitch==Width*4.
`generation` запоминает Sequence: повторная перерисовка не загружает те же pixels снова.
Device пересоздаётся при смене canvas size; ошибки Clear/Present сбрасывают GPU.
Автоматического CPU fallback и разветвлённого device-loss state machine здесь нет.

`imageQuad` возвращает четыре XYZRHW/UV vertices. Aspect, fit/fill/stretch, zoom,
mirror, flip, rotation меняют геометрию/UV, а не CPU image. D3D9 half-pixel offset
учтён координатами −0.5. Неверный порядок rotation/aspect меняет итоговый crop.

Shader выполняет sharpness через соседние texels, gamma, contrast/brightness,
saturation и clamp. Порядок и normalisation brightness/255 являются поведением
этой реализации, их изменение требует проверки изображений, даже при прежних полях UI.
Фильтры применяются только к локальному виду; JPEG для сети остаётся исходным.

## Снимки

F5 вызывает Snapshot на UI. Ставится reading, Paint рисует без Present, readback
копирует back buffer через system-memory surface. После чтения выполняется Present,
а независимая Go-копия преобразуется BGRA→RGBA и записывается PNG в горутине.
Именно поэтому снимок включает shader, crop, rotation и поля окна.
GPU readback может быть дорогим, но это действие пользователя, не каждый кадр.

## Что переносить

Отдельно выделяются decoder worker, ownership frame mailbox, renderer interface
и UI adapter. Конкретный shader/D3D требует Windows/GPU capabilities. Не выносите
GPU.Paint на произвольную горутину ради ускорения без пересмотра threading model.
Если нужны recording/analysis, они не должны возвращать borrowed Pixels после freePixels.

Проверки: WIC scale и GPU pixel readback в self-test, минимальный `--gpu-test`,
ручные F11/Esc/F5/resize/minimize. Автоматика не перебирает все GPU, DPI, фильтры,
device reset и screenshot formats; визуальный smoke нужен при изменении shader/quad.
