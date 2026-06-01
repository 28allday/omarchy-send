#!/usr/bin/env bash
#
# install.sh — install Omarchy-Send.
#
# Quick install (nothing to clone — the Once way):
#
#   curl -fsSL https://raw.githubusercontent.com/28allday/omarchy-send/main/install.sh | bash
#
# When run from a git clone it builds from source instead (if Go is present),
# otherwise it downloads the latest released binary for your architecture.
#
#   ./install.sh
#
# Environment overrides:
#   BIN_DIR=/usr/local/bin           install location (default ~/.local/bin)
#   OMARCHY_SEND_VERSION=v0.1.0       pin a release (default: latest)
#
# Behaviour:
#   - Headless system: installs the plain `omarchy-send` TUI binary.
#   - Omarchy desktop:  additionally adds a Walker entry that launches it as a
#     floating TUI (via the stock TUI.float app-id), like the Wi-Fi TUI.

set -euo pipefail

REPO="28allday/omarchy-send"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
APP_DIR="$HOME/.local/share/applications"
BIN="$BIN_DIR/omarchy-send"
VERSION="${OMARCHY_SEND_VERSION:-latest}"

mkdir -p "$BIN_DIR"

# If the script lives next to the source tree, we're in a clone.
SCRIPT_DIR=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi

# ---- obtain the binary ---------------------------------------------------
if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/go.mod" ] && command -v go >/dev/null 2>&1; then
  echo "==> Building omarchy-send from source..."
  (cd "$SCRIPT_DIR" && go build -o "$BIN" ./cmd/omarchy-send)
else
  # Download the released binary for this OS/arch (curl-style install).
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  if [ "$os" != "linux" ]; then
    echo "ERROR: omarchy-send ships Linux binaries only (detected: $os)." >&2
    echo "       On other systems, clone the repo and build with Go." >&2
    exit 1
  fi
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) echo "ERROR: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  asset="omarchy-send-${os}-${arch}"
  if [ "$VERSION" = "latest" ]; then
    url="https://github.com/$REPO/releases/latest/download/$asset"
  else
    url="https://github.com/$REPO/releases/download/$VERSION/$asset"
  fi

  echo "==> Downloading $asset ($VERSION)..."
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' EXIT
  if command -v curl >/dev/null 2>&1; then
    curl -fSL --proto '=https' --tlsv1.2 -o "$tmp" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$tmp" "$url"
  else
    echo "ERROR: need curl or wget to download the binary." >&2
    exit 1
  fi
  install -m 755 "$tmp" "$BIN"
fi
echo "    Installed: $BIN"

# ---- Omarchy desktop integration -----------------------------------------
# Only on Omarchy: add a Walker entry that opens the TUI in a floating window.
# The TUI.float app-id is matched by Omarchy's stock floating-window rule, so
# no Hyprland configuration is required.
if command -v omarchy-launch-tui >/dev/null 2>&1 || [ -d "$HOME/.local/share/omarchy" ]; then
  mkdir -p "$APP_DIR"

  # Install the bundled icon into the user's hicolor theme. The SVG is embedded
  # so the curl-piped install has nothing extra to fetch.
  icon_dir="$HOME/.local/share/icons/hicolor/scalable/apps"
  mkdir -p "$icon_dir"
  cat > "$icon_dir/omarchy-send.svg" <<'SVG'
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" width="256" height="256">
  <rect width="256" height="256" rx="56" fill="#16161e"/>
  <!-- paper-plane: upper wing (light) + lower flap (darker) -->
  <path d="M214 42 L42 114 L110 142 Z" fill="#7aa2f7"/>
  <path d="M214 42 L110 142 L130 214 L158 166 Z" fill="#5a7fd6"/>
</svg>
SVG
  gtk-update-icon-cache -q -t -f "$HOME/.local/share/icons/hicolor" 2>/dev/null || true

  cat > "$APP_DIR/omarchy-send.desktop" <<EOF
[Desktop Entry]
Name=Omarchy-Send
Comment=Send & receive files over the LAN (LocalSend-compatible)
Exec=xdg-terminal-exec --app-id=TUI.float -e $BIN
Icon=omarchy-send
Terminal=false
Type=Application
Categories=Network;FileTransfer;
Keywords=localsend;share;transfer;airdrop;
EOF
  echo "==> Omarchy detected — added floating Walker entry (with icon)."
  echo "    Launch it from Walker by searching 'Omarchy-Send'."

  # Nautilus right-click: "Send via Omarchy-Send". Same approach as Omarchy's
  # Transcode entry — a nautilus-python MenuProvider that opens the TUI in a
  # floating presentation terminal with the selected paths pre-staged. Installed
  # from the clone when present, otherwise written from an embedded copy so the
  # curl-piped install needs nothing extra.
  #
  # Desktop-only: skip entirely when Nautilus isn't installed (e.g. a headless
  # server). The right-click flow is a graphical convenience and is not required
  # there — the TUI and headless send still work without it.
  if command -v nautilus >/dev/null 2>&1; then
  ext_dir="$HOME/.local/share/nautilus-python/extensions"
  mkdir -p "$ext_dir"
  if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/nautilus/omarchy-send.py" ]; then
    cp "$SCRIPT_DIR/nautilus/omarchy-send.py" "$ext_dir/omarchy-send.py"
  else
    cat > "$ext_dir/omarchy-send.py" <<'PY'
import os
import shlex
import shutil

from gi import require_version

require_version("Nautilus", "4.1")

from gi.repository import GObject, Gio, Nautilus


# omarchy-send installs to ~/.local/bin, which is often NOT on the PATH the
# Nautilus process (and the terminal it spawns) inherits from the graphical
# session — so resolve it to an absolute path and invoke it by that.
def _resolve(name, fallbacks):
    found = shutil.which(name)
    if found:
        return found
    for path in fallbacks:
        if path and os.path.isfile(path) and os.access(path, os.X_OK):
            return path
    return None


def _binary():
    home = os.path.expanduser("~")
    fallbacks = []
    bin_dir = os.environ.get("BIN_DIR")
    if bin_dir:
        fallbacks.append(os.path.join(bin_dir, "omarchy-send"))
    fallbacks.append(os.path.join(home, ".local", "bin", "omarchy-send"))
    fallbacks.append(os.path.join(home, "bin", "omarchy-send"))
    return _resolve("omarchy-send", fallbacks)


def _wrapper():
    home = os.path.expanduser("~")
    fallbacks = [
        os.path.join(home, ".local", "share", "omarchy", "bin",
                     "omarchy-launch-floating-terminal-with-presentation"),
    ]
    return _resolve("omarchy-launch-floating-terminal-with-presentation", fallbacks)


class OmarchySendAction(GObject.GObject, Nautilus.MenuProvider):
    def _launch(self, paths):
        wrapper = _wrapper()
        binary = _binary()
        if not wrapper or not binary:
            return
        cmd = shlex.join([binary, *paths])
        Gio.Subprocess.new([wrapper, cmd], Gio.SubprocessFlags.NONE)

    def _selected_paths(self, files):
        paths = []
        seen = set()
        for file in files:
            location = file.get_location()
            if not location:
                continue
            path = location.get_path()
            if path and path not in seen:
                seen.add(path)
                paths.append(path)
        return paths

    def _make_item(self, paths):
        label = (
            "Send via Omarchy-Send"
            if len(paths) == 1
            else f"Send {len(paths)} items via Omarchy-Send"
        )
        item = Nautilus.MenuItem(
            name="OmarchySendNautilus::send",
            label=label,
            icon="omarchy-send",
        )
        item.connect("activate", self._on_activate, paths)
        return item

    def _on_activate(self, _menu, paths):
        self._launch(paths)

    def get_file_items(self, *args):
        files = args[0] if len(args) == 1 else args[1]
        if not _wrapper() or not _binary():
            return []
        paths = self._selected_paths(files)
        if not paths:
            return []
        return [self._make_item(paths)]
PY
  fi
  echo "==> Added Nautilus right-click entry 'Send via Omarchy-Send'."
  # Restart Nautilus so the extension loads (windows reopen on demand).
  nautilus -q >/dev/null 2>&1 || true
  fi # nautilus present
else
  echo "==> Headless system — installed as a plain TUI."
fi

echo
case ":$PATH:" in
  *":$BIN_DIR:"*) : ;;
  *) echo "Note: $BIN_DIR is not on your PATH. Add it, or run $BIN directly." ;;
esac
echo "Done. Run: omarchy-send"
