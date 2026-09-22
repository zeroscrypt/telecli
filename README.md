# telecli

Терминальный клиент Telegram с vim-подобной модальностью ввода. Написан на Go, протокольное ядро — [TDLib](https://github.com/tdlib/td) через прямой cgo-биндинг.

- Три панели: папки → чаты → лента сообщений, переключение `Tab`/стрелками/цифрами `1`/`2`/`3`.
- Режимы `Normal`/`Insert`/`Command`, как в vim.
- Живые обновления входящих сообщений без перезахода в чат.
- Многострочный черновик, отправка файлов (`ctrl+f`), внешний `$EDITOR` для длинных сообщений (`ctrl+e`).
- Отправка текста и файлов прямо из shell, без входа в TUI: `telecli send ...`.
- Настраиваемые горячие клавиши (`keybindings.toml`) и опции интерфейса (`settings.toml`).

## Установка

### Зависимости

- Go 1.25 или новее.
- Собранная из исходников TDLib (см. ниже — **не через пакетный менеджер**, это важно).
- Данные приложения Telegram (`api_id`/`api_hash`) — бесплатно регистрируются на [my.telegram.org](https://my.telegram.org).

### 1. Собрать TDLib

Пакетные версии TDLib (например, `brew install tdlib` на macOS) в лучшем случае устарели на несколько
лет и Telegram отклоняет вход с такой версией ошибкой `UPDATE_APP_TO_LOGIN`. Собирайте из актуального
`master`, не из старого тега.

**macOS:**

```sh
brew install cmake gperf openssl@3 zlib

git clone --depth 1 https://github.com/tdlib/td.git
cd td && mkdir build && cd build
cmake -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$HOME/.local/tdlib" \
      -DOPENSSL_ROOT_DIR=/opt/homebrew/opt/openssl@3 -DZLIB_ROOT=/opt/homebrew/opt/zlib ..
cmake --build . --target tdjson --target tdjson_static -- -j8
cmake --install . --prefix "$HOME/.local/tdlib"
```

После установки собранная из `master` `libtdjson` использует `@rpath`-относительное имя — без явного
`rpath` бинарник упадёт с `Library not loaded: @rpath/libtdjson....dylib`. Один раз после сборки
добавьте флаг `-Wl,-rpath,"${prefix}/lib"` в строку `Libs:` файла
`~/.local/tdlib/lib/pkgconfig/tdjson.pc` (после `-L...`, перед `-ltdjson`; `cmake --install` перезатрёт
этот файл при пересборке TDLib — патч нужно будет повторить).

**Linux (Ubuntu/Debian, не проверено физически, задокументировано по официальным источникам):**

```sh
sudo apt-get update && sudo apt-get install -y git cmake g++ openssl libssl-dev zlib1g-dev gperf

git clone --depth 1 https://github.com/tdlib/td.git
cd td && mkdir build && cd build
cmake -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=$HOME/.local/tdlib ..
cmake --build . --target tdjson --target tdjson_static -j$(nproc)
cmake --install . --prefix $HOME/.local/tdlib
```

Нужно ≥4 ГБ RAM на этапе компиляции. `.pc`-файл, вероятно, тоже нуждается в `-rpath`-патче — сверьте
по факту, если получите ошибку загрузки библиотеки при запуске.

### 2. Собрать telecli

```sh
git clone https://github.com/zeroscrypt/telecli.git
cd telecli
PKG_CONFIG_PATH="$HOME/.local/tdlib/lib/pkgconfig" go build -o telecli ./cmd/telecli
```

Если на машине также установлен старый пакетный TDLib — убедитесь, что
`$HOME/.local/tdlib/lib/pkgconfig` идёт первым в `PKG_CONFIG_PATH`, иначе можно случайно собраться со
старой версией.

### 3. Получить данные приложения Telegram

Зарегистрируйте приложение на [my.telegram.org](https://my.telegram.org) → «API development tools» →
получите `api_id` и `api_hash`. Передайте их один раз через переменные окружения при первом запуске —
дальше они сохранятся в системном хранилище учётных данных (Keychain на macOS, Secret Service на
Linux), запрашивать заново не придётся:

```sh
TELECLI_API_ID=12345678 TELECLI_API_HASH=abcdef0123456789abcdef0123456789 ./telecli
```

Если системное хранилище недоступно — данные сохраняются в
`<config dir>/telecli/config.toml` (macOS: `~/Library/Application Support/telecli/config.toml`,
Linux: `~/.config/telecli/config.toml`).

При первом запуске также потребуется пройти стандартную авторизацию Telegram (номер телефона, код
подтверждения, при необходимости — облачный пароль) — запрашивается интерактивно в терминале.

## Использование

```sh
telecli                                        # запустить TUI
telecli send @username -m "привет"             # отправить текст без входа в TUI
telecli send @username -f file.png -m "подпись"
telecli send 123456789 -m "по числовому chat_id"
telecli send -m "для канала" -- -1001234567890 # отрицательный chat_id — после "--"
```

## Горячие клавиши (Normal-режим, по умолчанию)

| Клавиша | Действие |
|---|---|
| `↑`/`k`, `↓`/`j` | навигация по списку в активной панели |
| `Tab` | переключить фокус (циклически) |
| `←`/`→` | фокус на соседнюю панель (без зацикливания) |
| `1` / `2` / `3` | прямой переход: папки / чаты / сообщения |
| `Enter` | выбрать/открыть |
| `Esc` | назад |
| `i` | войти в режим ввода (нужен выбранный чат) |
| `:` | командная строка (`:q`/`:quit`) |
| `ctrl+e` | открыть черновик во внешнем `$EDITOR` |
| `ctrl+f` | отправить файл (ввод пути) |
| `q`, `ctrl+c` | выход |

В режиме ввода: `Enter` — отправить, `ctrl+j` — перенос строки, `Esc` — отменить и выйти.

Все клавиши переопределяются в `<config dir>/telecli/keybindings.toml` (создаётся вручную, формат —
`toml`, поля — списки строк на действие, отсутствующие поля используют значения по умолчанию).

## Настройки интерфейса

`<config dir>/telecli/settings.toml`:

```toml
editor = "nano"           # приоритет: это поле → $EDITOR → vi
align_own_right = true    # прижимать свои сообщения к правому краю (по умолчанию включено)
```

## Лицензия

MIT — см. [LICENSE](LICENSE).
