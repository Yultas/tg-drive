# ⚡ TG-Drive

> **TG-Drive** — персональное безлимитное облачное хранилище на базе Telegram с поддержкой файлов до **4 ГБ** (Telegram Premium), монтированием виртуального сетевого диска (WebDAV) в Windows и Linux, и автоматической фоновой синхронизацией локальных папок.

---

## ✨ Возможности

* 🚀 **Поддержка файлов до 4 ГБ (Telegram Premium):** многопоточная загрузка частями (чанками по 512 КБ через `upload.saveBigFilePart`).
* 💾 **Монтирование диска:** встроенный легковесный WebDAV сервер и автоматическое подключение диска (например, `Z:` в Windows) без сторонних драйверов ядра.
* 🔄 **Автосинхронизация папок:** отслеживание событий файловой системы в реальном времени (`fsnotify`) с дебаунсом и очередью фоновой загрузки.
* ⚙️ **Фоновая системная служба (Daemon / Windows Service / Linux systemd):** автозапуск при старте системы без открытых терминальных окон.
* ⚡ **Чистый Go (Pure Go):** компактный бинарный файл (~18 МБ), потребление 15–30 МБ RAM, отсутствие CGO и лишних зависимостей.

---

## 🛠 Установка и сборка

### Требования
* Go 1.22+

### Сборка из исходников:
```bash
git clone https://github.com/Yultas/tg-drive.git
cd tg-drive
go mod download
go build -o tg-drive.exe .
```

---

## 🚀 Быстрый старт

### 1. Авторизация в Telegram
```bash
tg-drive auth
```
*(Введите номер телефона и код из Telegram. Сессия сохранится в `~/.tgdrive/session.json`)*.

### 2. Настройка папки для автосинхронизации
```bash
tg-drive sync add --local "C:\Users\User\Documents\CloudSync"
```

### 3. Запуск работы (консольный режим)
```bash
tg-drive run
```
*В Проводнике Windows автоматически появится сетевой диск `Z:`, а файлы из указанной папки начнут синхронизироваться в Telegram.*

### 4. Установка как системной службы (Windows / Linux)
```bash
tg-drive service install
tg-drive service start
```

### 5. Настройка прокси (опционально)
```bash
# SOCKS5
tg-drive proxy set socks5://127.0.0.1:1080
# или с авторизацией:
tg-drive proxy set socks5://user:password@1.2.3.4:1080

# HTTP / HTTPS
tg-drive proxy set http://127.0.0.1:8080

# MTProto Proxy
tg-drive proxy set "tg://proxy?server=1.2.3.4&port=443&secret=ee..."

# Проверка и отключение
tg-drive proxy status
tg-drive proxy clear
```

---

## 📖 Справка по командам CLI

| Команда | Описание |
| :--- | :--- |
| `tg-drive auth` | Вход в Telegram (QR / телефон / 2FA) |
| `tg-drive run` | Запуск сервера WebDAV, диска и синхронизации в консоли |
| `tg-drive mount [буква]` | Подключить виртуальный диск (например, `Y:`) |
| `tg-drive unmount [буква]` | Отключить виртуальный диск |
| `tg-drive sync add --local <путь>` | Добавить папку в список автосинхронизации |
| `tg-drive sync list` | Показать список отслеживаемых папок |
| `tg-drive proxy set <url>` | Настроить SOCKS5 / HTTP / MTProto прокси |
| `tg-drive proxy clear` | Отключить прокси (прямое подключение) |
| `tg-drive proxy status` | Показать текущий статус прокси |
| `tg-drive status` | Проверить текущий статус сервера, размер данных и диск |
| `tg-drive service install` | Установить как системную службу автозапуска |
| `tg-drive service start / stop` | Управление фоновой службой |

---

## 📄 Лицензия
MIT License
