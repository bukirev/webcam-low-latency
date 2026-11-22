# Карта автоматических тестов

Исходники: [protocol_test.go](../../go-version/protocol_test.go),
[interop_test.go](../../go-version/interop_test.go),
[enumeration_windows_test.go](../../go-version/enumeration_windows_test.go),
[diagnostics_test.go](../../go-version/diagnostics_test.go),
[adaptive_video_test.go](../../go-version/adaptive_video_test.go),
[discovery_test.go](../../go-version/discovery_test.go),
[sender_ports_windows_test.go](../../go-version/sender_ports_windows_test.go),
[teleport_windows_test.go](../../go-version/teleport_windows_test.go).

20 тестов пройдены в проверенной конфигурации Windows с Python-зависимостями.
Для наличия всех проверок сверяйте подробный вывод и причины пропусков.
Установщики проходят отдельные тесты в каталогах сборки.
[Последний прогон](../verification-log.md).

| Test | Что подтверждает | Чего не подтверждает |
|---|---|---|
| FragmentCompatibilityAndReordering | Header, большой JPEG payload, reorder, duplicate, seq wrap | Реальную JPEG картинку и LAN loss |
| AssemblerBoundsAndExpiry | Malformed packets, истёкший неполный frame | Fuzzing всех lengths и DOS envelope |
| PCMCompatibilityAndClipping | PCM header, limits, seq wrap, truncation | Звучание и физический output |
| RingBoundedAndSilentUnderrun | Overflow, сохранение свежего, zero fill, prebuffer | Непрерывную работу часов устройств |
| StreamingResamplerKeepsDuration | Split/whole equivalence, длительность нескольких rates | Alias suppression и субъективное качество |
| LatestMailboxNeverQueuesOldFrames | Последнее значение без накопления | Все возможные multi-producer interleavings |
| UDPVideoLoopback | SUB/fragment/assemble по реальному localhost UDP | Firewall, broadcast across LAN, MTU routers |
| ClientIgnoresUnusedSenderSettings | Role validation и конфликт портов | Все поля/типы/миграции Config |
| GoVideoToLegacyPython | Go sender → настоящий Python Receiver | Полный Python GUI и hardware capture |
| LegacyPythonVideoToGo | Python Sender → Go receiver → JPEG decode | GPU display и latency на втором ПК |
| LegacyPythonAudioCompatibility | PCM через настоящие Python pack/unpack | Сетевую передачу audio hardware |
| NativeEnumerationSurvivesThousandsOfFrames | 8 000 monitors + 2 000 windows, параллельность, cleanup | Любые другие native resource leaks |
| CrashEvidenceSurvivesBackgroundPanic | Fatal panic child process оставляет stack log | Все Windows fast-fail/native crash paths |
| VideoAdaptationHysteresis | Пороги, ступени, нижний лимит, медленное восстановление | Пропускную способность реальной сети |
| ReceiverReportsMissingVideoFrames | Отчёт приёмника по разрывам последовательности JPEG | Измерение сквозной задержки |
| VideoSenderAdaptsOverLossyUDP | Передатчик меняет ступень при синтетических потерях UDP | Wi‑Fi, VPN и поведение нескольких физических ПК |
| DiscoveryFindsMultipleSendersOnOnePort | Два отправителя обнаруживаются независимо | Broadcast в других подсетях |
| SenderReleasesVideoPortWhenAudioPortIsBusy | Ошибка bind аудио не оставляет видео-порт занятым | Внешние правила брандмауэра |
| OBSBridgeUDPToTeleport | Мост выдаёт тестовые данные в Teleport | Отрисовку физической камеры в прямом DLL-плагине |

## Почему enumeration test важен

Нужно выполнить больше регистраций, чем выдерживал старый design, а не просто
проверить один монитор. В актуальном коде callback постоянен; тест вызывает
реальные Enum API в четырёх workers и проверяет освобождение request contexts.
На desktop без мониторов он skip. Старый вариант в отдельной копии действительно
падал с тем же fatal, что и пользовательский сервер.

## Optional Python interop

Проверяется доступность python и modules network/cv2/numpy/audio_stream.
Процессы запускаются с cwd корня репозитория. При отсутствии зависимостей тесты
skip; приложение и runtime сборка от Python не зависят. Синтетические кадры
используются вместо записи пользовательской камеры. Слушающие sockets локальные.

## Как запускать

```powershell
Set-Location .\go-version
go test -count=1 -v ./...
go test -count=1 -run TestNativeEnumerationSurvivesThousandsOfFrames .
```

Если Go установлен локально, заменить `go` на `..\.tools\go\bin\go.exe`.
`-count=1` отключает cache, что важно при проверке живых Windows APIs.
Аппаратный self-test отдельный: [описание](selftest.md).
Для новой функции добавляйте проверку наблюдаемого контракта, а не копию её алгоритма.
Паника native/runtime в основном процессе не должна скрываться recover тестового runner.
