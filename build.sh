#!/usr/bin/env bash
# Builds netchat on Linux (Ubuntu/Debian and others).
#
#   ./build.sh              web UI + binary for this machine  -> bin/netchat
#   ./build.sh --skip-ui    reuse the already built UI (internal/webui/dist), no Node.js needed
#   ./build.sh --all        also cross-build Windows, Linux and macOS binaries
#   ./build.sh --install    build, then install for the current user:
#                           ~/.local/bin/netchat + app menu entry (+ autostart with --autostart)
#
# Needs: Go (see go.mod for the version); Node.js + npm unless --skip-ui.
set -euo pipefail
cd "$(dirname "$0")"

ALL=0 SKIP_UI=0 INSTALL=0 AUTOSTART=0
for arg in "$@"; do
  case "$arg" in
    --all) ALL=1 ;;
    --skip-ui) SKIP_UI=1 ;;
    --install) INSTALL=1 ;;
    --autostart) INSTALL=1 AUTOSTART=1 ;;
    -h|--help) sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $arg (see --help)" >&2; exit 2 ;;
  esac
done

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "error: '$1' not found. $2" >&2; exit 1; }
}

need go "Install Go from https://go.dev/dl/ (the apt package is often too old)."
GO_WANT=$(awk '/^go /{print $2; exit}' go.mod)
echo "go: $(go env GOVERSION) (go.mod wants >= go$GO_WANT; newer toolchains are fetched automatically)"

# ---- web UI (Svelte) -> internal/webui/dist, embedded into the binary ----
if [ "$SKIP_UI" = 0 ]; then
  need npm "Install Node.js (e.g. 'sudo apt install nodejs npm', or nodesource), or use --skip-ui."
  (
    cd web
    if [ ! -d node_modules ]; then npm ci; fi
    npm run build
  )
elif [ ! -f internal/webui/dist/index.html ]; then
  echo "error: --skip-ui but internal/webui/dist is empty; build the UI once (needs npm)." >&2
  exit 1
fi

# ---- Go binaries (pure Go, no cgo: the Linux tray icon talks D-Bus directly) ----
export CGO_ENABLED=0
LDFLAGS="-s -w"
mkdir -p bin

build() { # os arch output
  GOOS=$1 GOARCH=$2 go build -trimpath -ldflags "$LDFLAGS" -o "$3" ./cmd/netchat
  echo "built $3"
}

build "$(go env GOOS)" "$(go env GOARCH)" bin/netchat
if [ "$ALL" = 1 ]; then
  build linux amd64 bin/netchat-linux-amd64
  build linux arm64 bin/netchat-linux-arm64
  build windows amd64 bin/netchat.exe
  build windows arm64 bin/netchat-windows-arm64.exe
  build darwin arm64 bin/netchat-macos-arm64   # macOS without tray icon (tray needs a cgo build on a Mac)
  build darwin amd64 bin/netchat-macos-amd64
fi

# ---- optional per-user install (no sudo) ----
if [ "$INSTALL" = 1 ]; then
  BIN_DIR="$HOME/.local/bin"
  APP_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
  ICON_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/scalable/apps"
  mkdir -p "$BIN_DIR" "$APP_DIR" "$ICON_DIR"
  install -m 0755 bin/netchat "$BIN_DIR/netchat"
  install -m 0644 web/public/favicon.svg "$ICON_DIR/netchat.svg"
  cat > "$APP_DIR/netchat.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=netchat
Comment=LAN chat for humans and agents
Exec=$BIN_DIR/netchat
Icon=netchat
Terminal=false
Categories=Network;Chat;
EOF
  echo "installed $BIN_DIR/netchat and an app menu entry"
  if [ "$AUTOSTART" = 1 ]; then
    AUTO_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/autostart"
    mkdir -p "$AUTO_DIR"
    sed 's|^Exec=.*|Exec='"$BIN_DIR"'/netchat --no-open|' "$APP_DIR/netchat.desktop" > "$AUTO_DIR/netchat.desktop"
    echo "netchat will start (in the tray) when you log in"
  fi
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) echo "note: $BIN_DIR is not in PATH; log out and back in, or add it to your shell profile" ;;
  esac
fi

cat <<'EOF'

Run:  bin/netchat            (tray icon + opens the chat in your browser)
      bin/netchat serve      (headless)
If ufw is enabled, allow the LAN port once (47800: older netchat peers):
      sudo ufw allow 27800/udp && sudo ufw allow 27800/tcp && sudo ufw allow 47800/udp
EOF
