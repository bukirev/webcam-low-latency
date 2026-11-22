# capture_windows.go — экран, GDI и перечисление устройств

[Исходник](../../go-version/capture_windows.go) · [Разбор аварии](../lessons-learned.md)

## Координаты и выбор источника

`CaptureRect{X,Y,W,H,Name}` описывает физические пиксели виртуального desktop.
У монитора слева/сверху X/Y отрицательные. `monitors` получает прямоугольники
EnumDisplayMonitors, `windowDevices` — видимые верхние окна и их заголовки.
Saved HWND не является устойчивым идентификатором после закрытия/перезапуска окна.

`screenRect` разрешает current config:

| capture_kind | Геометрия |
|---|---|
| Монитор | Выбранный индекс; при недопустимом — первый доступный |
| Все мониторы | Виртуальный desktop через GetSystemMetrics |
| Область | Явные left/top/width/height |
| Окно (видимая область) | GetWindowRect; свёрнутое/несуществующее окно даёт ошибку |

Ширина/высота должны быть 1…16384. Проверка не задаёт общий пиксельный лимит GDI
capture, поэтому крайние допустимые размеры могут потребовать много памяти.
Это копирование видимых пикселей, не offscreen window capture: перекрытия сохраняются.

## Постоянные callbacks

`monitorCallback` и `windowCallback` создаются один раз при инициализации package.
Каждое перечисление создаёт `enumerationResult`, кладёт его в sync.Map под новым ID,
передаёт только ID как LPARAM и удаляет запись после синхронного Enum API.
Callback находит Go-результат по ID, копирует native RECT/имя и добавляет в свой slice.
Параллельные вызовы имеют разные контексты; завершённые результаты registry не удерживает.

`syscall.NewCallback` внутри `monitors()` недопустим для покадрового использования:
регистрации живут до завершения процесса, а не до GC замыкания. Именно это вызвало
аварию preview 1. Стабильная функция без захваченных результатов отделяет lifetime
native entry point от lifetime отдельного запроса.

## Capture lifecycle

`captureScreen` создаёт WIC factory на своём COM thread, desktop DC, compatible DC
и top-down 32-bit DIB. Отрицательная BiHeight задаёт порядок строк сверху вниз.
Память DIB принадлежит bitmap handle, не Go GC.

При изменении W/H старый bitmap выводится из DC, удаляется, создаётся новый DIB.
BitBlt с SRCCOPY|CAPTUREBLT копирует область; optional drawCursor добавляет курсор.
EncodeBGRA синхронно получает DIB address и отдаёт новый JPEG. На обычном кадре
DC/DIB не пересоздаются. Удалять выбранный в DC bitmap до SelectObject(old) нельзя.

Период равен `1/fps - время работы итерации`; отрицательный остаток не вызывает sleep.
Это best effort FPS, а не real-time scheduler. Pause проверяется раз в 100 ms.
Ошибка выбранного окна отображается и повторно проверяется через секунду.
Серьёзная ошибка захвата/кодирования завершает worker, после чего retry запускает заново.

`drawCursor` учитывает hotspot. GetIconInfo выделяет bitmap handles mask/color;
они освобождаются DeleteObject, даже когда основной курсор принадлежит Windows.

## Пределы и повторное использование

Есть JPEG quality и max_width, но нет dirty rectangles, Desktop Duplication,
hardware screen encoder, bitrate adaptation или обхода защищённого видео.
Screen encode выполняется и без сетевых клиентов, если сеанс capture запущен.
Preview может дополнительно декодировать собственный JPEG — выключение preview
уменьшает локальную работу, но не прекращает screen encode.

Для другого проекта стоит выделить typed RegionResolver, GDI lifetime owner
и encoder sink; callbacks registry можно использовать как общий pattern только
для синхронных enumeration API. Для async callbacks удалять контекст сразу нельзя.

Проверки: 8 000 monitor и 2 000 window enumerations (4 параллельных worker),
проверка cleanup registry, нативный screen JPEG в self-test, последующая передача UDP.
