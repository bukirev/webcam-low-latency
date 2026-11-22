# diagnostics_windows.go — журналы и crash evidence

[Исходник](../../go-version/diagnostics_windows.go) · [Разбор аварии](../lessons-learned.md)

## Запуск

`startDiagnostics` вызывается в main до UI и workers. Создаёт `logs` рядом с EXE;
при ошибке создания directory пробует `%LOCALAPPDATA%/WebcamGo/logs`. Имя файла
`session-YYYYMMDD-HHMMSS.mmm-PID.log`, открытие O_EXCL не перезаписывает прошлую аварию.
Если directory существует, но сам OpenFile запрещён, функция просто возвращает
пустой cleanup: это ограничение текущего fallback, не полная гарантия логирования.

Standard log направлен в файл, включены microseconds, START с version/Go/PID.
`debug.SetTraceback("all")` и `debug.SetCrashOutput` добавляют аварийный stack.
Runtime держит duplicate file handle. Нормальный возврат main вызывает cleanup:
отменяет heartbeat, пишет CLEAN EXIT и Sync. `os.Exit`, принудительное завершение
и некоторые native fast-fail пути могут обойти нормальное завершение.

Механизм сохраняет доказательства, но не перезапускает программу и не гарантирует
получение полного stack при любой форме heap corruption/kill. Не следует скрывать
fatal под blanket recover или молча объявлять отсутствие журнала отсутствием сбоя.

## Health

Раз в 20 секунд отдельная горутина записывает NumGoroutine, HeapAlloc/HeapSys,
число GC и GDI/USER resource counts GetGuiResources. Эти данные помогают различить
накопление горутин, Go heap и Win32 handles. Это не total private bytes, не GPU RAM,
не load в процентах и не число зарезервированных callback slots.

Если heap стабилен, всё равно возможен callback exhaustion. Именно так выявилась
ошибка перечисления мониторов. Последняя goroutine в stack также не обязательно
является причиной: читать нужно fatal message и первый аварийный стек.

## Retention и содержимое

Сортируются только `session-*.log`, сохраняются до 20 имён; старые удаляются best effort.
Это лимит сеансов, не размера одного файла. Активный текущий файл не удаляется;
при нескольких процессах могут остаться старые открытые файлы.
Автоматической отправки наружу нет. Кадры и PCM в log не пишутся, но crash stack
может содержать локальные пути и технические адреса — журнал не следует коммитить
или публиковать как исходник.

## Использование и проверки

`TestCrashEvidenceSurvivesBackgroundPanic` запускает test executable в отдельном
процессе, вызывает настоящий panic фоновой горутины и проверяет, что после смерти
дочернего процесса в temp log сохранились panic text и source stack.
Родительский test runner продолжает работу. Тест не эмулирует все native fast-fails.

Для другой программы удобно переносить последовательность «установить логирование
до создания workers → уникальный сеанс → crash output → редкие health aggregates».
Paths, retention, редактирование чувствительных данных и интеграцию с UI следует
сделать параметрами, а не глобальными свойствами пакета.
