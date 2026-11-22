# region_windows.go — выбор области мышью

[Исходник](../../go-version/region_windows.go)

## Путь данных

UI читает draft → разрешает выбранный монитор → делает единичный screenshot →
показывает modal Dialog со вписанным bitmap → получает прямоугольник мыши →
переводит его в desktop coordinates → меняет draft → ждёт общего Apply.
Выбор области сам не переключает работающий capture и не сохраняет профиль.

`screenBitmap` создаёт desktop/compatible DC, top-down DIB, выполняет BitBlt,
копирует BGRA в Go RGBA и создаёт walk.Bitmap. Временные GDI handles освобождаются
после копирования, возвращённый bitmap удаляет вызывающий код после диалога.
Снимок монитора на диск не пишется.

## Координаты

Есть три пространства: физические пиксели виртуального desktop, исходного monitor
bitmap и canvas. PaintPixels вычисляет `shown`, чтобы сохранить aspect и учесть
поля. Walk MouseEvent здесь приходит в native pixels; нельзя повторно применять
коэффициент DPI как к логическим координатам.

Для X: `desktopX = monitorX + (selectionX-shownX)*monitorWidth/shownWidth`.
Для ширины: `selectionWidth*monitorWidth/shownWidth`; аналогично Y/H.
Округление целочисленное, минимальный итоговый размер — один пиксель.
Отрицательное monitorX нормально. Начало и конец выделения ограничиваются shown,
drag в обратном направлении нормализуется через min/abs.

## Взаимодействие и lifetime

SetCapture сохраняет drag при выходе мыши за widget, ReleaseCapture завершает его.
Кнопка подтверждения включается лишь для ненулевого прямоугольника. Esc/отмена
не изменяют draft. Modal loop принадлежит UI, bitmap и pen живут всё время Run.

Пока capture источник всегда снимок выбранного монитора, даже если до открытия
была выбрана область/окно. Это облегчает выбор нового region; это не full-desktop
overlay и не selection across нескольких мониторов одним растягиванием.
Ресайз диалога после выделения, разный DPI мониторов, выход/потеря mouse capture
требуют ручной проверки: полного автоматического UI покрытия нет.

## Повторное использование

Полезно отделить чистую coordinate conversion от GUI и тестировать её на
negative origins, reverse drag, letterboxing и DPI. В другом toolkit нужно заново
проверить единицы mouse events: перенос одной формулы без этого создаёт систематический offset.
GDI capture/bitmap lifetime можно заменить платформенным screenshot adapter,
сохранив модель «подтверждённое выделение меняет draft, Apply меняет работу».
