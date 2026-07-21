#!/usr/bin/env bash
set -euo pipefail

REPO="${PVE_REPO:-}"
BASE_URL="${PVE_BASE_URL:-}"
VERSION="${PVE_VERSION:-latest}"
PREFIX="${HOME}/.local/bin"
SYSTEM=0
TOKEN="${PVE_TOKEN:-}"
NODES="${PVE_NODES:-}"
NO_CONFIG=0
ASSUME_YES=0

usage() {
  cat <<'EOF'
Usage: install.sh [options]

Options:
  --repo owner/name             GitHub repo for releases
  --base-url https://...        Self-hosted update server
  --version vX.Y.Z              Version to install (default: latest)
  --token 'PVEAPIToken=...'     Proxmox API token
  --nodes 'ip1,ip2'             Proxmox node IPs or hostnames
  --system                      Install to /usr/local/bin (requires root)
  --user                        Install to ~/.local/bin (default)
  --no-config                   Skip configuration prompts
  -y, --yes                     Accept defaults without prompting
  -h, --help                    Show this help

Environment variables:
  PVE_REPO, PVE_BASE_URL, PVE_VERSION, PVE_TOKEN, PVE_NODES

Examples:
  curl -fsSL https://raw.githubusercontent.com/h4sht/pve/main/scripts/install.sh | bash
  curl -fsSL ... | bash -s -- --repo h4sht/pve --nodes 10.0.0.10,10.0.0.11
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)      REPO="$2"; shift 2;;
    --base-url)  BASE_URL="$2"; shift 2;;
    --version)   VERSION="$2"; shift 2;;
    --token)     TOKEN="$2"; shift 2;;
    --nodes)     NODES="$2"; shift 2;;
    --system)    SYSTEM=1; shift;;
    --user)      SYSTEM=0; shift;;
    --no-config) NO_CONFIG=1; shift;;
    -y|--yes)    ASSUME_YES=1; shift;;
    -h|--help)   usage; exit 0;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 1;;
  esac
done

if [[ $SYSTEM -eq 1 ]]; then
  if [[ $EUID -ne 0 ]]; then
    echo "--system requires root." >&2
    exit 1
  fi
  PREFIX="/usr/local/bin"
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$OS" in
  linux) OS=linux;;
  darwin) OS=darwin;;
  *) echo "Unsupported OS: $OS" >&2; exit 1;;
esac
case "$ARCH" in
  x86_64|amd64) ARCH=amd64;;
  arm64|aarch64) ARCH=arm64;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1;;
esac

ASSET="pve-${OS}-${ARCH}"

if [[ -z "$BASE_URL" && -n "$REPO" ]]; then
  BASE_URL="https://github.com/${REPO}/releases/download"
fi
if [[ -z "$BASE_URL" ]]; then
  echo "No --repo or --base-url provided. Provide one or set PVE_REPO/PVE_BASE_URL." >&2
  usage >&2
  exit 1
fi

if [[ "$BASE_URL" == *"/releases/download"* ]]; then
  if [[ -z "$REPO" ]]; then
    echo "--repo is required when using GitHub Releases style base URL." >&2
    exit 1
  fi
  DOWNLOAD_URL="${BASE_URL}/${VERSION}/${ASSET}"
else
  DOWNLOAD_URL="${BASE_URL%/}/${VERSION}/${ASSET}"
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

umask 077
command -v curl >/dev/null || { echo "curl is required." >&2; exit 1; }

curl -fsSL --retry 3 -o "${TMPDIR}/${ASSET}" "$DOWNLOAD_URL" || {
  echo "Download failed: ${DOWNLOAD_URL}" >&2
  exit 1
}
chmod +x "${TMPDIR}/${ASSET}"

mkdir -p "$PREFIX"
install -m 0755 "${TMPDIR}/${ASSET}" "${PREFIX}/pve"

echo "Installed pve to ${PREFIX}/pve"

if [[ $NO_CONFIG -eq 0 ]]; then
  CFG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/pve"
  CFG_FILE="${CFG_DIR}/config.json"
  mkdir -p "$CFG_DIR"
  chmod 700 "$CFG_DIR"

  NODES_JSON=""
  if [[ -n "$NODES" ]]; then
    IFS=',' read -ra NODE_ARR <<< "$NODES"
    for n in "${NODE_ARR[@]}"; do
      n="$(echo "$n" | xargs)"
      [[ -z "$n" ]] && continue
      if [[ -z "$NODES_JSON" ]]; then
        NODES_JSON="\"$n\""
      else
        NODES_JSON="$NODES_JSON, \"$n\""
      fi
    done
  fi

  cat > "$CFG_FILE" <<EOF
{
  "nodes": [${NODES_JSON}],
  "token": "${TOKEN}",
  "repo": "${REPO}",
  "base_url": "${BASE_URL}"
}
EOF
  chmod 600 "$CFG_FILE"
  echo "Wrote configuration to ${CFG_FILE}"
fi

echo "Done. Run 'pve --help' to get started."
