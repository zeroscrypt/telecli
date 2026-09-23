#!/usr/bin/env bash
# Устанавливает telecli: определяет платформу, скачивает подходящий бинарник
# последнего релиза, кладёт его в каталог из $PATH под именем "telecli" (без
# суффикса платформы — человеку не нужно об этом думать).
#
# Использование:
#   curl -fsSL https://raw.githubusercontent.com/zeroscrypt/telecli/public/install.sh | sh
# или, чтобы сначала посмотреть на скрипт:
#   curl -fsSL https://raw.githubusercontent.com/zeroscrypt/telecli/public/install.sh -o install.sh
#   less install.sh && sh install.sh

set -eu

repo="zeroscrypt/telecli"

os="$(uname -s)"
arch="$(uname -m)"

case "$os-$arch" in
  Darwin-arm64)
    asset="telecli-darwin-arm64"
    ;;
  Linux-x86_64)
    asset="telecli-linux-amd64"
    ;;
  *)
    echo "Готового бинарника для $os/$arch нет (сейчас собираются только macOS Apple Silicon и Linux x86_64)." >&2
    echo "Собери из исходников — см. раздел \"Build from source\" в README." >&2
    exit 1
    ;;
esac

url="https://github.com/$repo/releases/latest/download/$asset"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

echo "Скачиваю $asset..."
curl -fsSL -o "$tmp" "$url"
chmod 755 "$tmp"

# Куда установить: первый каталог из этого списка, который уже есть в $PATH
# и доступен на запись без sudo — в порядке предпочтения. Не берём
# /usr/local/bin автоматом, если он не в PATH или не пишется без sudo -
# лучше явно попросить человека добавить $HOME/.local/bin в PATH самому, чем
# молча просить пароль там, где он не ожидает.
install_dir=""
for candidate in "$HOME/.local/bin" "/opt/homebrew/bin" "/usr/local/bin"; do
  case ":$PATH:" in
    *":$candidate:"*)
      if [ -d "$candidate" ] && [ -w "$candidate" ]; then
        install_dir="$candidate"
        break
      fi
      ;;
  esac
done

if [ -z "$install_dir" ]; then
  install_dir="$HOME/.local/bin"
  mkdir -p "$install_dir"
  echo ""
  echo "Внимание: $install_dir не найден в \$PATH — добавь в ~/.zshrc (или ~/.bashrc):"
  echo "  export PATH=\"$install_dir:\$PATH\""
  echo "и перезапусти терминал (или выполни эту строку прямо сейчас)."
  echo ""
fi

mv "$tmp" "$install_dir/telecli"
trap - EXIT

echo "telecli установлен: $install_dir/telecli"
echo "Запусти командой: telecli"
