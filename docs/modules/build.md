# build.ps1, app.manifest, go.mod/go.sum — сборка

[Скрипт](../../go-version/build.ps1) · [Manifest](../../go-version/app.manifest)
· [go.mod](../../go-version/go.mod) · [go.sum](../../go-version/go.sum)

## Вход и выход

Скрипт запускают из корня: `powershell -ExecutionPolicy Bypass -File .\go-version\build.ps1`.
Параметр `-Go` задаёт executable toolchain; при значении `go` скрипт сначала
использует локальный `.tools/go/bin/go.exe`, если он есть. Toolchain — для сборки,
пользователю на другом ПК он не нужен.

Последовательность: CGO_ENABLED=0 → go test → pinned rsrc → build → README/BAT →
dependency licenses → allowlist ZIP → размер и SHA256.
Выход: `dist/go-preview/WebcamGo/WebcamGo.exe` и `release/WebcamGo-Windows-x64.zip`.
EXE собирается `-trimpath -ldflags '-H=windowsgui -s -w'`.

Windows GUI subsystem скрывает обычную консоль, поэтому stderr нельзя считать
достаточным пользовательским журналом. Это причина наличия diagnostics_windows.go.
Trimpath убирает пути сборки из Go metadata; strip уменьшает бинарник. Это не
обфускация, подпись, защита от модификаций или гарантия одинаковых байтов любой сборки.

## Зависимости

| Модуль | Назначение |
|---|---|
| lxn/walk, lxn/win | Native GUI и Win32 types/wrappers |
| gonutz/d3d9 | Direct3D9 COM wrappers |
| degubites/go-wca | WASAPI interfaces |
| go-ole | GUID, VARIANT и COM types/helpers |
| x/sys, govaluate | Транзитивные зависимости GUI/interop |
| akavel/rsrc v0.10.2 | Инструмент сборки ресурсов, не runtime dependency |

Точные версии закреплены go.mod/sum. Зависимости не объявляются универсально
современными/безошибочными: при обновлении снова проверяют ownership wrappers,
особенно PROPVARIANT.String. Native DLL загружаются из Windows; отдельные OpenCV,
FFmpeg и Python DLL Go-бинарнику не нужны. Нужны системные MF/WIC/WASAPI/D3D и GPU driver.

## Manifest

Common Controls v6 нужен Walk, architecture amd64 соответствует release target.
requestedExecutionLevel=asInvoker не требует elevation. DPI awareness объявлен
PerMonitorV2 с fallback true/pm. Это декларация DPI, а не доказательство, что
каждый math/UI path уже протестирован на смешанном DPI.
rsrc создаёт app.syso; он генерируемый и не хранится в Git.

Скрипт передаёт `rsrc -arch amd64`, но не задаёт явно GOARCH. Сборку следует
выполнять в Windows amd64 toolchain environment и проверять target; он не является
готовым универсальным cross-compilation dispatcher.

## Упаковка

Лицензии берутся из установленных module directories. Copy-Item -Force позволяет
повторную сборку поверх readonly license copies из Go module cache.
ZIP включает только EXE, README, три START BAT и licenses. Settings/logs/snapshots,
toolchain и временные проверки туда не попадают, даже если лежат рядом в dist.
BAT меняет cwd к своей папке и запускает EXE с client/server/local.

Публикация GitHub не входит в build.ps1. Перед release отдельно проверяют содержимое,
SHA256 и распакованный binary. Не следует пересобирать stable Python ZIP ради
изменения комментариев/документации Go. Изменения docs публикуются как source commit.
