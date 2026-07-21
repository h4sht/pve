#!/usr/bin/env bash
# pv uninstaller

set -euo pipefail

PREFIX="${1:-}"
if [[ -z "$PREFIX" ]]; then
  if [[ -d "$HOME/.local/bin" && -x "$HOME/.local/bin/pv" ]]; then
    PREFIX="$HOME/.local/bin"
  elif [[ -x "/usr/local/bin/pv" ]]; then
    PREFIX="/usr/local/bin"
  else
    PREFIX="$(dirname "$(command -v pv 2>/dev/null || echo "$HOME/.local/bin/pv")")"
  fi
fi

BIN="$PREFIX/pv"
echo "  · quitando binario: $BIN"
if [[ -e "$BIN" ]]; then
  rm -f "$BIN"
fi

CFG="${XDG_CONFIG_HOME:-$HOME/.config}/pv"
echo "  · quitando config:  $CFG"
rm -rf "$CFG"

echo
echo "✔ pv desinstalado. (el bloque PATH añadido a tu ~/.bashrc/.zshrc lo tendrás que quitar a mano.)"
