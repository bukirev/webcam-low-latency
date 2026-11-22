# Лицензия WebcamGo Direct

Код плагина WebcamGo Direct распространяется на условиях GNU General Public
License версии 2 или любой более поздней версии (SPDX: GPL-2.0-or-later).
Полный текст GPL версии 2 находится в [COPYING](COPYING).

Условия относятся к следующим исходникам плагина:

- `obs-plugin/plugin.go`, `obs-plugin/bridge.h`, `obs-plugin/module.c`;
- `go-version/protocol.go`, `go-version/audio.go`, `go-version/audio_receive_windows.go`,
  `go-version/broadcast_windows.go`, `go-version/session.go`, `go-version/native_windows.go`,
  которые компилируются в DLL плагина.

Сторонние компоненты сохраняют свои лицензии. Для сборки используется
[libobs из OBS Studio 30.1.2](https://github.com/obsproject/obs-studio/tree/30.1.2/libobs).
Перечень зависимостей Go закреплён в `go-version/go.mod` и `go-version/go.sum`.
Сборка DLL описана в [README](README.md).
