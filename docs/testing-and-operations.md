# Проверки, эксплуатация и сопровождение

## Перед работой

Определить ветку реализации: `go-version` или Python 4.3. Проверить Git status,
не трогать пользовательские `settings_*.json`, Go profiles, logs и snapshots.
Запущенный пользователем сервер не требуется перезапускать ради комментариев.

Windows-only пакеты и тесты выполняются на Windows. Toolchain сейчас закреплён
директивой `go 1.27.4`; не следует заявлять поддержку произвольного старого Go.
При чистом clone resources генерирует build.ps1, app.syso в Git отсутствует.

## Автоматическая проверка

Из каталога go-version:

```powershell
go test -count=1 -v ./...
```

Локальный toolchain из корня: `.\.tools\go\bin\go.exe`;
из go-version — `..\.tools\go\bin\go.exe`.
Проверить не только итог PASS, но и SKIP optional Python tests.
Подробная карта контрактов — [tests.md](modules/tests.md).

Legacy тесты из корня: `python -m unittest discover -s tests -v`.
Они требуют установленных requirements и в части случаев Tk/Windows APIs.
Не импортируйте camera.py ради списка функций: этот файл является GUI entry point.
Для inventory используйте AST/чтение файла.

## Аппаратный smoke

Собрать через `go-version/build.ps1`, распаковать ZIP в отдельную directory,
запустить EXE `--self-test`, прочитать go-self-test.json со всеми `*_error`.
Реальный source может быть занят уже работающим приложением. Краткий успешный
тест не означает многочасовой проверки hotplug, форматов или качества звука.

Ручной сценарий: выбрать client/server/local → Apply → Start/Stop каждого потока →
Start уже активного → изменить draft без Apply → Apply live field → Apply restart field →
сменить роль → F11/Esc/F5 → resize/minimize → выключить preview → закрыть Viewer/панель.
Особенно проверять client: source fields не должны отвлекать пользователя.

## Длительная передача

Для воспроизведения проблем считать отдельно:

- Время жизни server process, не только то, что окно ещё видно.
- Продвижение camera/screen seq и количество audio packets/samples.
- FPS/resolution/quality/decode scale и число подключённых клиентов.
- Heap, goroutines, native handles и возраст данных в очередях.
- Ошибки driver/codec/network и время освобождения при Stop.

Во время preview 2 исправления был подтверждён uptime >6 min и 240 s получения
всех трёх UDP-потоков на localhost. Для нового выпуска с иными изменениями
это историческое свидетельство, а не заменяющий текущую проверку сертификат.
Если нужна проверка суток, планировать именно сутки и включать hotplug/idle/load cycles.

## Разбор симптомов

| Симптом | Сначала проверить |
|---|---|
| Закрылись все окна | Последний session log, CLEAN EXIT, fatal stack, version EXE |
| `too many callback functions` | Не осталась ли preview 1; NewCallback в hot path |
| Чёрное окно, процесс жив | Status D3D/WIC, preview, source timeout, драйвер, формат |
| Нет кадров сети | Host/ports, SUB, firewall private network, sender subscribers |
| Звук прерывается | Endpoint format, block/buffer_ms, source status, overruns/underruns |
| Слышен echo | Mic + playback loopback, отсутствие AEC, направление звукового маршрута |
| Растёт память | Heap vs native/GPU, размер кадра, partial frames, handles и ownership |
| Stop задерживается | Синхронное чтение драйвера; context не прерывает произвольный native call |

Не объявлять причину по последней строке stack. Выделить аварийную горутину,
native API, lifetime и нагрузочный путь. По возможности получить минимальный
reproducer и доказать, что он падает на старом коде и проходит на новом.

## Выпуск

1. Проверить diff, версии и соответствие docs; выполнить относящиеся tests.
2. Собрать standalone ZIP, проверить allowlist и absence profiles/logs/media.
3. Проверить binary из распакованного ZIP; при необходимости SHA256 совпадение с tested EXE.
4. Для preview создать отдельный prerelease, не заменять stable 4.3 без решения о выпуске.
5. Сверить GitHub asset state/size/digest, target commit и download URL.

Документация и комментарии сами по себе не требуют нового бинарного release:
их достаточно сохранить и опубликовать в исходниках. Общий GitHub README должен
вести к актуальной документации и правильной версии архива.
