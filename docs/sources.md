# Первичные источники и проверка контрактов

Описание поведения проекта опирается прежде всего на его исходники и тесты.
Следующие официальные документы нужны при изменении Windows/Go boundaries.
Индексы vtable и layout структур сверяются с Windows SDK, не угадываются по примеру.

| Тема | Источник | Где применяется |
|---|---|---|
| NewCallback lifetime | [Go syscall.NewCallback](https://pkg.go.dev/syscall?GOOS=windows#NewCallback) | screen/window enumeration, panel timer |
| Pinning | [Go runtime.Pinner](https://pkg.go.dev/runtime#Pinner) | nativeArgs/invoke/com |
| Pointer rules | [Go unsafe.Pointer](https://pkg.go.dev/unsafe#Pointer) | uintptr/native addresses/lifetime |
| OS thread | [Go runtime.LockOSThread](https://pkg.go.dev/runtime#LockOSThread) | UI и COM workers |
| Crash output | [Go runtime/debug.SetCrashOutput](https://pkg.go.dev/runtime/debug#SetCrashOutput) | Журнал fatal stack |
| COM apartments | [Initializing the COM Library](https://learn.microsoft.com/en-us/windows/win32/learnwin32/initializing-the-com-library) | Создание/балансировка COM thread |
| Media Foundation source reader | [IMFSourceReader](https://learn.microsoft.com/en-us/windows/win32/api/mfreadwrite/nn-mfreadwrite-imfsourcereader) | Camera capture |
| WIC bitmap memory | [CreateBitmapFromMemory](https://learn.microsoft.com/en-us/windows/win32/api/wincodec/nf-wincodec-iwicimagingfactory-createbitmapfrommemory) | Encode BGRA |
| WIC source copying | [IWICBitmapSource](https://learn.microsoft.com/en-us/windows/win32/api/wincodec/nn-wincodec-iwicbitmapsource) | BGRA decode, dimensions |
| WASAPI loopback | [Loopback Recording](https://learn.microsoft.com/en-us/windows/win32/coreaudio/loopback-recording) | System audio capture |
| Capture buffers | [IAudioCaptureClient](https://learn.microsoft.com/en-us/windows/win32/api/audioclient/nn-audioclient-iaudiocaptureclient) | Native packet lifetime |
| Direct3D device | [IDirect3DDevice9](https://learn.microsoft.com/en-us/windows/win32/api/d3d9/nn-d3d9-idirect3ddevice9) | Textures, shader, Present |

## Зависимости как часть контракта

Список версий хранится в [go.mod](../go-version/go.mod), hashes — в
[go.sum](../go-version/go.sum). При анализе wrapper нужно читать именно используемую
версию исходника, а не только страницу «latest».

- [go-wca](https://github.com/degubites/go-wca): PROPVARIANT.String и WASAPI wrappers.
- [go-ole](https://github.com/go-ole/go-ole): GUID/VARIANT и COM types.
- [d3d9](https://github.com/gonutz/d3d9): Windows x64 native calls и структуры.
- [Walk](https://github.com/lxn/walk), [Win](https://github.com/lxn/win): UI threading,
  layout и native event conversion.
- [rsrc](https://github.com/akavel/rsrc): ресурсы manifest при сборке.

Внутренние PROPBAG2 и WAVEFORMATEXTENSIBLE offsets, MF GUID/slots и shader ABI
особенно чувствительны к architecture changes. Не переносить на другую ABI
без проверки размеров, alignment, указателей и структуры native результата.
