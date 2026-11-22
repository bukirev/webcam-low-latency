# audio.go — PCM, кольцевой буфер и resampling

[Исходник](../../go-version/audio.go) · [Wire format](../protocols.md)

## Единицы измерения

Frame — один момент времени со всеми каналами. Stereo frame содержит два samples.
Sample здесь — один float32, normalised примерно в −1…1. PCM16 sample занимает 2 bytes.
Для 48 kHz stereo 10 ms: 480 frames = 960 samples = 1920 PCM bytes.
Столько не помещается в один payload 1200, поэтому блок делится на несколько packets.

`AudioPacket` хранит Session/Seq/Rate/Channels/Frames и декодированный interleaved
Samples. `parseAudio` проверяет magic, размеры, rates, channels и точное равенство
payload длине до чтения данных. Некорректный packet не попадает в playback.

## Кодирование PCM

`pcmPackets` ограничивает размер целыми frames, пишет header big-endian и PCM
little-endian. Клиппинг −1…32767/32768 предотвращает wrap положительного int16.
Каждому packet соответствует свой seq; между ними не требуется video-like assembler.
Выходной packet выделяется отдельно, поэтому отправка не переиспользует один общий
буфер, как video fragments.

`decodeNative/encodeNative` переводят endpoint representations: PCM8, PCM16,
PCM24, PCM32 или float32. PCM24 требует знакового расширения старшего байта.
Это не универсальный parser любого WAVEFORMATEXTENSIBLE: формат, alignment и
предположения о layout проверяет вызывающий Windows adapter.

## FloatRing

Ring ограничен **числом samples** и защищён mutex. Push удаляет старейшее при
переполнении. Pop(n,minimum,keep) возвращает slice ровно n, реальную count и
нулевую недостающую часть. Если накоплено меньше minimum, данные ещё не потребляются.
Если размер больше keep>0, старое отбрасывается до keep перед чтением.

Например, 40 ms stereo при 48 kHz — `48000*2*40/1000 = 3840` samples.
Нельзя поставить capacity=1920, думая, что это 40 ms для того же stereo.
Ring не знает channels: producer/consumer обязаны сами сохранять выравнивание.
Она также не хранит timestamps и не восстанавливает позиции пропущенных блоков.

## Resampler

Создаётся с from/to rates и числом output channels. Process получает input samples
и sourceChannels, сначала делает downmix/expansion, затем линейную интерполяцию.
Mono получается средним каналов; для расширения доступные каналы повторяются.
При одинаковой частоте интерполяция не нужна.

Для разных rates сохраняются pending и дробная position. Последний frame предыдущего
блока нужен для перехода к следующему без повторного старта фазы. Пересоздание
resampler на каждый блок даёт ошибки длительности и разрывы.

Это простой low-latency алгоритм. Здесь нет профессионального anti-alias filter,
phase-quality modes, adaptive clock drift correction или limiter. Для речи и
LAN preview он служит базовым преобразователем; для музыкального/студийного
применения нужно оценить спектр, aliasing и качество отдельно.

## Перенос

PCM/ring/resampler — наиболее самостоятельная часть package main: только standard
library, без Windows. Выделяя пакет, задайте typed units, допустимые channels/rates,
ownership slices и семантику overflow. Не переносите float ring как очередь
видеокадров и не заменяйте её Latest из video path.

Проверки: header/clipping/truncation; bounded ring/silence/prebuffer; сравнение
одного длинного resample и множества блоков с контролем длительности для нескольких rates.
Тесты не оценивают слуховое качество и дрейф реальных устройств в течение часов.
