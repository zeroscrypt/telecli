[🇷🇺 Русский](README.ru.md) | 🇬🇧 English

# telecli

A terminal Telegram client with vim-like modal input. Written in Go, protocol core is [TDLib](https://github.com/tdlib/td) via direct cgo bindings.

- Three panes: folders → chats → message feed, switch with `Tab`/arrows/number keys `1`/`2`/`3`.
- `Normal`/`Insert`/`Command` modes, vim-style.
- Live updates for incoming messages without re-opening the chat.
- Multi-line draft, file sending (`ctrl+f`), external `$EDITOR` for long messages (`ctrl+e`).
- Send text and files straight from the shell, without entering the TUI: `telecli send ...`.
- Configurable keybindings (`keybindings.toml`) and interface options (`settings.toml`).

## Installation

### Dependencies

- Go 1.25 or newer.
- TDLib built from source (see below — **not from a package manager**, this matters).
- Telegram application credentials (`api_id`/`api_hash`) — register for free at [my.telegram.org](https://my.telegram.org).

### 1. Build TDLib

Packaged versions of TDLib (e.g. `brew install tdlib` on macOS) are at best several years out of
date, and Telegram rejects login with such a version with `UPDATE_APP_TO_LOGIN`. Build from current
`master`, not an old tag.

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

After installing, the `libtdjson` built from `master` uses an `@rpath`-relative name — without an
explicit `rpath`, the binary fails with `Library not loaded: @rpath/libtdjson....dylib`. Once after
building, add the `-Wl,-rpath,"${prefix}/lib"` flag to the `Libs:` line of
`~/.local/tdlib/lib/pkgconfig/tdjson.pc` (after `-L...`, before `-ltdjson`; `cmake --install`
overwrites this file on rebuild — the patch needs to be reapplied then).

**Linux (Ubuntu/Debian, not physically verified, documented from official sources):**

```sh
sudo apt-get update && sudo apt-get install -y git cmake g++ openssl libssl-dev zlib1g-dev gperf

git clone --depth 1 https://github.com/tdlib/td.git
cd td && mkdir build && cd build
cmake -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=$HOME/.local/tdlib ..
cmake --build . --target tdjson --target tdjson_static -j$(nproc)
cmake --install . --prefix $HOME/.local/tdlib
```

Needs ≥4 GB RAM during compilation. The `.pc` file likely also needs the `-rpath` patch — check in
practice if you get a library-loading error at runtime.

### 2. Build telecli

```sh
git clone https://github.com/zeroscrypt/telecli.git
cd telecli
PKG_CONFIG_PATH="$HOME/.local/tdlib/lib/pkgconfig" go build -o telecli ./cmd/telecli
```

If an old packaged TDLib is also installed on the machine, make sure
`$HOME/.local/tdlib/lib/pkgconfig` comes first in `PKG_CONFIG_PATH`, otherwise you might accidentally
build against the old version.

### 3. Get Telegram application credentials

Register an application at [my.telegram.org](https://my.telegram.org) → "API development tools" →
get `api_id` and `api_hash`. Pass them once via environment variables on first run — they'll be
saved to the system credential store afterward (Keychain on macOS, Secret Service on Linux), no need
to enter them again:

```sh
TELECLI_API_ID=12345678 TELECLI_API_HASH=abcdef0123456789abcdef0123456789 ./telecli
```

If the system credential store is unavailable, the credentials are saved to
`<config dir>/telecli/config.toml` (macOS: `~/Library/Application Support/telecli/config.toml`,
Linux: `~/.config/telecli/config.toml`).

On first run you'll also need to go through standard Telegram authorization (phone number,
confirmation code, cloud password if set up) — prompted interactively in the terminal.

## Usage

```sh
telecli                                        # launch the TUI
telecli send @username -m "hello"              # send text without entering the TUI
telecli send @username -f file.png -m "caption"
telecli send 123456789 -m "by numeric chat_id"
telecli send -m "for a channel" -- -1001234567890 # negative chat_id — after "--"
```

## Keybindings (Normal mode, defaults)

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | navigate the list in the focused pane |
| `Tab` | cycle focus between panes |
| `←`/`→` | focus the neighboring pane (no wraparound) |
| `1` / `2` / `3` | jump directly to: folders / chats / messages |
| `Enter` | select/open |
| `Esc` | back |
| `i` | enter insert mode (needs a selected chat) |
| `:` | command line (`:q`/`:quit`) |
| `/` | search chats/channels/contacts |
| `t` | "about" screen — full list of keybindings |
| `d` | leave/delete the chat under the cursor (with confirmation) |
| `ctrl+e` | open the draft in an external `$EDITOR` |
| `ctrl+f` | send a file (path prompt) |
| `q`, `ctrl+c` | quit |

In insert mode: `Enter` sends, `ctrl+j` inserts a newline, `Esc` cancels and exits.

All keybindings can be overridden in `<config dir>/telecli/keybindings.toml` (create it manually,
`toml` format, fields are lists of key strings per action; missing fields fall back to defaults).

## Interface settings

`<config dir>/telecli/settings.toml`:

```toml
editor = "nano"           # priority: this field → $EDITOR → vi
align_own_right = true    # right-align your own messages (enabled by default)
```

## Update check

On launch, `telecli` silently checks in the background whether a newer version is available (this
repository's GitHub Releases) — if found, `vX.Y.Z → vX.Y.Z+1 (:update)` appears on the right side of
the bottom line. To check manually and see the result explicitly, use the `:update` command in
Normal mode (`:` → `update` → `Enter`). The command only shows that a newer version is available —
there's no automatic binary replacement yet, update by rebuilding (see "Installation" above) or
downloading the new release from the Releases page.

## Releases

Versions are assigned automatically by [Release Please](https://github.com/googleapis/release-please)
based on [Conventional Commits](https://www.conventionalcommits.org/) in the `main` branch history:

| Commit prefix | Result |
|---|---|
| `fix: ...` | patch (0.1.0 → 0.1.1) |
| `feat: ...` | minor (0.1.0 → 0.2.0) |
| `feat!: ...` / `fix!: ...` (or a `BREAKING CHANGE:` footer) | major (0.1.0 → 1.0.0) |

On every commit to `main`, Release Please updates an open Release PR with the accumulated
`CHANGELOG.md` and the next version; merging that PR creates the git tag and GitHub Release itself.
Official binaries built with `-ldflags "-X main.version=vX.Y.Z"` are published to the release
separately, not automatically by this workflow.

## License

MIT — see [LICENSE](LICENSE).
