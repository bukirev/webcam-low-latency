# Журнал выпусков VB2Link

Проверено 23 ноября 2026 по времени Екатеринбурга (UTC+05:00). Здесь записаны опубликованные версии, исходные коммиты и состав выпусков. Результаты тестов и их охват описаны в [журнале проверок](verification-log.md).

## Текущий выпуск Windows x64

[VB2Link 0.1.0-preview.2](https://github.com/bukirev/webcam-low-latency/releases/tag/suite-v0.1.0-preview.2) опубликован 23 ноября 2026 в 00:10:25 UTC+05:00. Тег `suite-v0.1.0-preview.2` закреплён на [8d920f4](https://github.com/bukirev/webcam-low-latency/commit/8d920f4af8bb267dfbded3a5c5afb4719ce4e1c3). В набор входят передатчик **0.1.0-preview.2** и плагин OBS **0.1.0-preview.4**.

- [Скачать единый установщик](https://github.com/bukirev/webcam-low-latency/releases/download/suite-v0.1.0-preview.2/VB2Link-Setup-Windows-x64.exe). В одном окне выбираются передатчик, плагин или оба компонента. Установщик содержит оба пакета и запускается без Python.
- Передатчик получил компактную панель камеры, экрана и звука, управляемый предпросмотр, ступенчатую адаптацию видео и оформление VB2Link. Отключение предпросмотра освобождает локальную обработку картинки, передача продолжается.
- Плагин принимает потоки прямо в OBS. Каждый источник OBS выбирает свой компьютер и порт. Новые имена отображения сохраняют зарегистрированные ID источников, используемые в существующих сценах.
- Менеджер сверяет протоколы при установке, обновлении и откате. Имя проекта на странице по умолчанию обновлено коммитом [4548e73](https://github.com/bukirev/webcam-low-latency/commit/4548e73d379cf9d0bd0e868b3982c44dc0daad92). URL репозитория сохраняет работоспособность канала старых менеджеров.

Релиз предварительный. GitHub по ссылке `releases/latest` по-прежнему отдаёт стабильный Python 4.3. Плагин 0.1.0-preview.4 поставляется в общем выпуске. У старых отдельных менеджеров собственные каналы. Переход на общий канал описан в [инструкции](webcamgo-updates.md).

## Опубликованные файлы

Файлы скачаны по публичным URL. Для каждого проверены размер и SHA-256 из GitHub, а также побайтовое совпадение с локальной копией из каталога выпуска.

| Имя файла | Байт | SHA-256 |
|---|---:|---|
| `VB2Link-Setup-Windows-x64.exe` | 18570752 | `a1ecfdef168f2bdcba1d6b006b9fae4ebbab1445796ed40cb6eea157416396fd` |
| `WebcamGo-Compatibility.json` | 6108 | `299447a3a03155d032b2c9a7f303c3eaab6fc2c89941b75ffef4a116f91884c5` |
| `WebcamGo-OBS-Direct-Licenses.zip` | 28288 | `167c16a6d729670905d6cb0a7519b2822272cc3e946b2fd92045da7afe75777f` |
| `WebcamGo-OBS-Direct-Update-Windows-x64.zip` | 1191842 | `3bfc2efd323b772181e6d7d499a2d14b248a0612370cad8a26164701be28ecd2` |
| `WebcamGo-Sender-Licenses.zip` | 15200 | `67099fff0db46fcc065235a4529c98e7b7e295ef9df1dfcbcafa6a3c99159357` |
| `WebcamGo-Sender-Update-Windows-x64.zip` | 4497750 | `81a5367319bd9281a50e143bbafb6467fd92e3ef42135b6460289272755ab433` |

## История переходов

| Тег или коммит | Изменение |
|---|---|
| [ede7b5f](https://github.com/bukirev/webcam-low-latency/commit/ede7b5f) | Исходная Python-реализация камеры. |
| [v3.0.1](https://github.com/bukirev/webcam-low-latency/releases/tag/v3.0.1), `21d80eb` | Переносимый Windows-пакет для запуска без установленного Python. |
| [v4.0.0](https://github.com/bukirev/webcam-low-latency/releases/tag/v4.0.0), `965a812` | Захват экрана и микс системного звука с микрофоном. |
| [v4.1.0](https://github.com/bukirev/webcam-low-latency/releases/tag/v4.1.0), `4a3dda1` | Снижение нагрузки приёмника за счёт числа декодирований, размера JPEG и повторного использования изображения. |
| [v4.2.0](https://github.com/bukirev/webcam-low-latency/releases/tag/v4.2.0), `9524fb5` | Единая Python-панель и управление обоими дочерними окнами. |
| [v4.3.0](https://github.com/bukirev/webcam-low-latency/releases/tag/v4.3.0), `01f738b` | Фильтрация настроек по роли и одна кнопка применения. |
| [v5.0.0-go.1](https://github.com/bukirev/webcam-low-latency/releases/tag/v5.0.0-go.1), `6c9152e` | Go-реализация Windows x64 с Media Foundation, WIC, Direct3D и WASAPI. |
| [v5.0.0-go.2](https://github.com/bukirev/webcam-low-latency/releases/tag/v5.0.0-go.2), `634d513` | Исправление падения при многократной регистрации Win32 callback во время захвата экрана. |
| [0ce6ec9](https://github.com/bukirev/webcam-low-latency/commit/0ce6ec9) | Документация Go/Python, диагностика, карта модулей и перенос решений. |
| [v5.0.0-obs.1](https://github.com/bukirev/webcam-low-latency/releases/tag/v5.0.0-obs.1), `218b29b` | Мост UDP → OBS Teleport. |
| [v5.0.0-obs.2](https://github.com/bukirev/webcam-low-latency/releases/tag/v5.0.0-obs.2), `07b3add` | Исправления панели и обработки конфликтов портов. |
| [v5.0.0-obs.3](https://github.com/bukirev/webcam-low-latency/releases/tag/v5.0.0-obs.3), `f71cbd9` | Скрипт создания сцен и источников OBS. |
| [v5.0.0-obs.4](https://github.com/bukirev/webcam-low-latency/releases/tag/v5.0.0-obs.4), `b9447b8` | Проверка JPEG на пути к OBS, включая обработку MJPG камеры. Ветка `obs-teleport`. |
| [obs-direct-v0.1.0-preview.1](https://github.com/bukirev/webcam-low-latency/releases/tag/obs-direct-v0.1.0-preview.1), `54e3de9` | Нативные источники OBS, C ABI, общий сетевой Go-код и прямой вывод в libobs. |
| [obs-direct-v0.1.0-preview.2](https://github.com/bukirev/webcam-low-latency/releases/tag/obs-direct-v0.1.0-preview.2), `2a8dbee` | Обнаружение нескольких передатчиков и отдельный выбор для каждого источника. |
| [suite-v0.1.0-preview.2](https://github.com/bukirev/webcam-low-latency/releases/tag/suite-v0.1.0-preview.2), `8d920f4` | Первый публичный единый установщик, политика совместимости, адаптация и бренд VB2Link. |
| [4548e73](https://github.com/bukirev/webcam-low-latency/commit/4548e73) | Обновление страницы проекта в ветке `main`. |

## Запись следующего выпуска

Для каждого выпуска фиксируйте тег, полный Git SHA, время с часовым поясом, статус, версии компонентов, список изменённых протоколов, имена и хеши файлов. Отдельно записывайте реально проверенные сценарии, пропуски и оборудование. Уже выпущенный тег и файлы используются обновляторами как неизменяемый контракт. Исправление бинарного пакета требует нового номера версии. Изменения руководств публикуются отдельными исходными коммитами.
