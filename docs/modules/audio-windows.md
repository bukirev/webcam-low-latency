# audio_windows.go — WASAPI capture, mix, transport и playback

Приём UDP вынесен в [audio_receive_windows.go](../../go-version/audio_receive_windows.go), чтобы его мог использовать плагин OBS без подключения WASAPI.

[Исходник](../../go-version/audio_windows.go) · [Алгоритмы](audio-core.md)

## Устройства и ownership

`audioDevices(flow)` возвращает default option и active endpoints. flow=0 — render,
flow=1 — capture. Выбор хранит endpoint ID; для импортированных Python-профилей
`selectEndpoint` также ищет friendly name. Default разрешается в момент открытия;
notification-driven смена default device во время работы не реализована.

`endpointName` следует ownership закреплённого go-wca 0.4.1: PROPVARIANT.String
копирует и освобождает LPWSTR. Повторный Clear после него приводил к double free.
Эту особенность надо пересмотреть при замене wrapper, а не переносить вслепую.

`audioEndpoint` владеет enumerator, IMMDevice, IAudioClient и выделенным mix format.
Service capture/render освобождается до endpoint.Close, затем Stop/Release client,
CoTaskMemFree format, Release device/enumerator. Всё на COM thread worker.

## openAudio и captureAudio

Открытие shared stream использует реальный endpoint mix format, его sample rate,
channels, bits и признак float. WAVEFORMATEXTENSIBLE проверяется по subtype.
Запрашиваемая buffer duration — `max(20, block_ms*2)` ms, фактический размер
возвращает WASAPI; эта величина не равна latency от capture до динамиков.

Для системного звука открывается **render endpoint** с LOOPBACK, затем берётся
IAudioCaptureClient. Для mic — capture endpoint без loopback. Это соответствует
[модели WASAPI loopback](https://learn.microsoft.com/en-us/windows/win32/coreaudio/loopback-recording).

Capture polling: ticker `max(2,block/2)` ms → все доступные packets → GetBuffer →
копия/тишина по flags → native decode → resample → FloatRing → ReleaseBuffer.
Заимствованный native pointer не должен покинуть итерацию. Event-driven WASAPI
и notifications endpoint здесь не используются.

## sendAudio

Для включённых system/mic создаются отдельные capture workers и rings примерно
на 100 ms samples. Независимые clock domains приводятся к сетевым rate/channels.
Mix ticker запрашивает block samples; для rates с дробным числом frames на block
используется остаток `rate*block_ms / 1000`, а не постоянное округление.

Gain каждого источника читается live. Отсутствующий материал дополняется нулями,
send_mute обнуляет mix; encoder клиппирует перед PCM16. SUB reader передаёт адреса
через bounded channel в единственного владельца map клиентов. Lease — 5 секунд.
В local mode захват работает, но в динамики и LAN mix не идёт.

Нет automatic echo cancellation, ducking, noise suppression, compression/limiting
или adaptive drift compensation. Два clocks могут постепенно расходиться;
bounded rings удалят избыток, но не выполнят плавную коррекцию темпа.

## receiveAudio / receiveAudioPackets

UDP receiver проверяет header, IP/Session и seq, отправляет packets в FIFO 64.
При переполнении удаляет старое. Первый packet задаёт формат и Session playback;
изменение Session/rate/channels отменяет прежний playback и создаёт новый ring/worker.
Сетевая rate — свойство sender, локальный endpoint может иметь другую rate.

Пакеты не переставляются по времени; поздний seq удаляется. Нельзя считать это
RTP/jitter buffer или обещанием точной A/V sync. При переносе следует также
сделать форматный snapshot каждого playback worker явным: не захватывать меняемую
переменную ring в отложенных замыканиях при дальнейших рефакторингах.

## playbackAudio

При старте создаётся source ring с prebuffer `buffer_ms`. Endpoint работает
в своём mix format, поэтому нужен resampler и второй ring нативных остатков.
GetCurrentPadding определяет свободные endpoint frames. Недостающие frames
пересчитываются через отношение rates с ceil, затем умножаются на channels.

После первоначального накопления материал читается; при отставании старое сверх
target + примерно 20 ms удаляется. Нехватка даёт нули и повторное накопление.
Volume/receive_mute читаются live. GetBuffer/ReleaseBuffer ограничивают native lifetime.

## Ошибки и проверки

Обычный возврат source/playback повторяет retryMedia. Native hang/fatal не ловится.
Сетевые инициализационные ошибки попадают в Status; формат/устройство меняется через restart.

Автотесты покрывают PCM/ring/resampler и Python packet compatibility. Self-test
открывает output, кратко запускает mic/loopback и muted playback. Пустой loopback
при отсутствии воспроизведения допустим; качество mix двух реальных устройств,
эхо и многочасовой drift требуют отдельных испытаний.
