# native_windows.go — ABI, COM и WIC

[Исходник](../../go-version/native_windows.go) · [Первичные источники](../sources.md)

## Зачем нужен слой

Нативные Windows API вызываются без Cgo. `invoke` оборачивает DLL procedure,
`com` читает vtable и вызывает метод с interface pointer первым аргументом.
Go helpers не проверяют правильность GUID, native signature или индекса метода.
Ошибка может повредить память, даже если компилятор Go доволен.

Реализация целится в **Windows x64**, использует размеры/padding его структур.
Нельзя считать её готовой 32-bit/ARM библиотекой. Числа в com — слоты интерфейса,
включая IUnknown QueryInterface=0, AddRef=1, Release=2, не номера из документации C++.

## Указатели и threads

`ptr` возвращает unsafe.Pointer. В `nativeArgs` явно переданные pointers удерживаются
Pinner до выхода syscall; args удерживаются KeepAlive. Числовой uintptr, содержащий
Go-адрес, сам по себе не сохраняет lifetime. Не превращайте все pointers в uintptr
раньше wrapper и не рассчитывайте на то, что «GC пока не запускался».

Это синхронное закрепление. Native API, сохраняющее pointer, имеет иной контракт.
Pinner не закрепляет рекурсивно всё графовое содержимое произвольной Go-структуры.
Поля-указатели, вложенные allocations и async completion требуют отдельного разбора.

`comThread`: LockOSThread → CoInitializeEx(MTA) → работа → CoUninitialize → Unlock.
Если получен RPC_E_CHANGED_MODE, уже есть другой apartment; helper не балансирует
его чужим CoUninitialize. Это допускает вызов из уже инициализированного UI thread,
но не отменяет требований конкретного COM-класса и marshaling между apartments.

## Таблица используемых WIC слотов

| Интерфейс | Слоты |
|---|---|
| IWICImagingFactory | DecoderFromStream 4; Encoder 8; Converter 10; Scaler 11; BitmapFromMemory 20 |
| IWICBitmapDecoder | GetFrame 13 |
| IWICBitmapSource | GetSize 3; CopyPixels 7 |
| Converter / Scaler | Initialize 8 |
| IWICBitmapEncoder | Initialize 3; CreateNewFrame 10; Commit 11 |
| IWICBitmapFrameEncode | Initialize 3; SetSize 4; SetPixelFormat 6; WriteSource 11; Commit 12 |
| IPropertyBag2 | Write 4 |
| IStream | Read 3; Seek 5 |

`hr` проверяет signed младшие 32 bits как HRESULT. Не все COM методы возвращают
HRESULT: ID3DBlob GetBufferPointer/GetBufferSize возвращают указатель/размер,
поэтому shader code намеренно не использует полученную ошибку hr для этих значений.

## JPEG decode

Go JPEG bytes → SHCreateMemStream → WIC decoder → frame → проверка размеров →
необязательный scaler → BGRA converter → CopyPixels в Go buffer.
Допустимо не более 16384 по стороне и 40 млн пикселей; проверка предшествует
выделению большого BGRA. Scale обычно 1/2/4 выбирает decoder caller.
Итог плотно упакован: stride=Width*4, порядок каналов BGRA.

WIC scaler не следует автоматически считать гарантированным hardware/reduced-IDCT
decode конкретного JPEG codec. Это контракт выходного размера, не измерение его CPU.
Все временные COM references освобождаются defer. Наружу возвращается PixelFrame,
который владеет памятью до freePixels/передачи Viewer.

## JPEG encode

Вход — заимствованный адрес плотно упакованного BGRA с положительным stride.
Владелец удерживает источник до возврата. Создаются WIC bitmap, optional scaler,
BGR converter, HGlobal stream, JPEG encoder, frame и ImageQuality property bag.
После Commit кадра и encoder поток перематывается, JPEG копируется в новый Go slice.
Предел output stream — 6 MiB, но сетевой предел меньше: см. [протокол](../protocols.md).

`propBag` повторяет PROPBAG2 x64; VARIANT использует VT_R4 с float32 quality/100.
Нельзя менять порядок полей или произвольно освобождать вложенные указатели.

## Владение

| Объект | Кто завершает lifetime |
|---|---|
| WIC.factory | `WIC.Close` на владельце COM thread |
| decoder/frame/scaler/converter/encoder/stream/bag | defer Release внутри операции |
| PixelFrame.Pixels | Decoder → Viewer → ровно один freePixels |
| sync.Pool item | Cache, не обязательное хранилище и не строгая RAM quota |
| EncodeBGRA data | Вызывающий код, wrapper его не освобождает |

Тесты: аппаратный self-test encode/decode, размера scale и GPU readback.
При переносе необходимы дополнительные negative tests повреждённых JPEG,
конкретных ABI и allocator boundaries; общая автоматическая ABI-верификация отсутствует.
