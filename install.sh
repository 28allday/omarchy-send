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
#   OMARCHY_SEND_MODE=local|remote    skip the local/remote prompt (default: ask,
#                                     or local when non-interactive)
#
# Behaviour:
#   - Local machine (home/LAN): installs the TUI; on Omarchy also adds a Walker
#     entry + the Nautilus right-click integration.
#   - Remote server (public IP): same install, then restricts port 53317 to the
#     Tailscale interface in the firewall, so it's reachable over the tailnet
#     only — not the open internet.

set -euo pipefail

REPO="28allday/omarchy-send"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
APP_DIR="$HOME/.local/share/applications"
BIN="$BIN_DIR/omarchy-send"
VERSION="${OMARCHY_SEND_VERSION:-latest}"
PORT=53317

mkdir -p "$BIN_DIR"

# If the script lives next to the source tree, we're in a clone.
SCRIPT_DIR=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi

# ---- local vs remote -----------------------------------------------------
# A remote (public-IP) server should not expose the transfer port to the
# internet. Ask once; default to "local" when non-interactive (e.g. piped
# `curl | bash` with no terminal) so a firewall is never changed without intent.
# Reads /dev/tty so the prompt still works under curl|bash.
MODE="${OMARCHY_SEND_MODE:-}"
case "$MODE" in
  local | remote) : ;; # explicit override, don't ask
  *)
    MODE="local"
    # Try to open the controlling terminal read-write on fd 3. A bare -r test
    # isn't enough: /dev/tty can exist yet fail to open (no controlling tty —
    # cron/CI/piped). Only prompt when the open actually succeeds.
    if { exec 3<>/dev/tty; } 2>/dev/null; then
      printf 'Install type — [L]ocal machine (home/LAN) or [r]emote server (public IP)? [L/r] ' >&3 || true
      IFS= read -r _ans <&3 || _ans=""
      exec 3>&- 3<&- || true
      case "$_ans" in
        r | R | remote | Remote | REMOTE) MODE="remote" ;;
      esac
    fi
    ;;
esac

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

# ---- agent context (AGENTS.md / CLAUDE.md) -------------------------------
# Install a context file so any AI agent on this machine (Claude, etc.) knows
# what omarchy-send is and — crucially — WHERE files land when other devices
# send them here. The canonical copy lives in the config dir as AGENTS.md (the
# cross-agent standard) with a CLAUDE.md symlink beside it; a short managed
# section is also added to ~/.claude/CLAUDE.md so it's known in every session.
CFG_DIR="$HOME/.config/omarchy-send"
mkdir -p "$CFG_DIR"

# Resolve the real receive directory: the config's receiveDir wins, else default.
RECV_DIR="$HOME/Omarchy-Send"
if [ -f "$CFG_DIR/config.json" ]; then
  _rd="$(grep -oE '"receiveDir"[[:space:]]*:[[:space:]]*"[^"]*"' "$CFG_DIR/config.json" \
    | sed -E 's/.*:[[:space:]]*"([^"]*)"/\1/' | head -n1 || true)"
  [ -n "${_rd:-}" ] && RECV_DIR="$_rd"
fi

# AGENTS.md — quoted heredoc (so backticks stay literal); @@RECV_DIR@@ filled after.
cat > "$CFG_DIR/AGENTS.md" <<'DOC'
# Omarchy-Send — context for AI agents

**What it is:** Omarchy-Send is a terminal (TUI) file-transfer tool installed on
this machine. It speaks the LocalSend protocol, so phones, desktops, and other
servers can send **files** and **plain-text messages** to this machine over the
local network or a Tailscale tailnet. It can also send outbound.

**Where received files live —** incoming files are saved under the *receive
directory*:

    @@RECV_DIR@@

That is the current value; the authoritative source is the `receiveDir` key in
`~/.config/omarchy-send/config.json` — read it if unsure. Details:

- A file still transferring has a temporary `.part` suffix and is renamed to its
  real name only when complete. Treat `*.part` files as incomplete — skip them.
- Filename collisions are de-duplicated (e.g. `photo (1).jpg`).
- A sent *folder* is recreated as a subdirectory tree under the receive dir.
- Plain-text **messages** are not written to disk — they appear in the TUI's
  Messages tab while the receiver is running.

**How it runs:** files are received only while a receiver is running — it is a
foreground TUI, not a background daemon. Start it with:

    omarchy-send

On a headless box, run it inside a TTY (tmux, or `ssh -t`). It listens on TCP
port **53317**. Auto-accept and an optional PIN live in the config / Settings tab.

**You can SEND messages from the CLI** — no TUI, no TTY, works from scripts and
agents. To message another device (e.g. to notify the user on their desktop):

    omarchy-send -to "<device alias>" -message "<text>"

Add `-send-pin <pin>` if the target requires a PIN, and `-wait 30s` to allow
longer for discovery. Works over the LAN and Tailscale alike; exit code 0 means
delivered. To send files, pass paths instead (`omarchy-send <file>…`) — that
opens the TUI with them staged, so it needs a TTY.

**Config:** `~/.config/omarchy-send/config.json`
(keys: `alias`, `receiveDir`, `port`, `autoAccept`, `pin`, `knownPeers`, …).

**If asked to "find / process what was just sent":** look in the receive
directory above and skip any `*.part` files (still transferring).
DOC
sed -i "s|@@RECV_DIR@@|$RECV_DIR|g" "$CFG_DIR/AGENTS.md"
ln -sf AGENTS.md "$CFG_DIR/CLAUDE.md"
echo "==> Wrote agent context: $CFG_DIR/AGENTS.md (+ CLAUDE.md symlink)."

# Managed, idempotent section in the user-global Claude memory.
CLAUDE_MD="$HOME/.claude/CLAUDE.md"
mkdir -p "$HOME/.claude"
[ -f "$CLAUDE_MD" ] || : > "$CLAUDE_MD"
BEGIN_MARK="<!-- BEGIN omarchy-send (managed by installer) -->"
END_MARK="<!-- END omarchy-send (managed by installer) -->"

# Build the fresh block (placeholder substituted) in a temp file.
blk="$(mktemp)"
cat > "$blk" <<'BLK'
<!-- BEGIN omarchy-send (managed by installer) -->
## Omarchy-Send (installed on this machine)

Omarchy-Send is a LocalSend-compatible terminal file-transfer tool; other devices
send files/messages to this box over LAN/Tailscale (TCP 53317). **Files sent here
land in `@@RECV_DIR@@`** (authoritative: the `receiveDir` key in
`~/.config/omarchy-send/config.json`). Files still transferring carry a `.part`
suffix — skip them. Text messages appear in the TUI's Messages tab, not on disk.
Receiving requires the TUI running (`omarchy-send`; use tmux or `ssh -t` when
headless). **You can SEND a message to another device from the CLI** (no TUI/TTY,
fine for scripts and agents): `omarchy-send -to "<alias>" -message "<text>"`
(plus `-send-pin <pin>` if the target requires one); exit 0 = delivered. Works
over LAN and Tailscale. Full notes: `~/.config/omarchy-send/AGENTS.md`.
<!-- END omarchy-send (managed by installer) -->
BLK
sed -i "s|@@RECV_DIR@@|$RECV_DIR|g" "$blk"

# Strip any prior managed block, then append the fresh one (no duplicates on re-run).
new_cm="$(mktemp)"
awk -v b="$BEGIN_MARK" -v e="$END_MARK" '
  $0==b {skip=1} skip && $0==e {skip=0; next} !skip' "$CLAUDE_MD" > "$new_cm"
# Drop a trailing blank line then re-add exactly one before the block, for tidiness.
{ cat "$new_cm"; printf '\n'; cat "$blk"; } > "$CLAUDE_MD"
rm -f "$blk" "$new_cm"
echo "    Added an Omarchy-Send section to $CLAUDE_MD."

# ---- firewall posture ----------------------------------------------------
# Shared by the remote-mode lockdown below and the local-mode public-IP warning.
#
# Tailscale interface: usually tailscale0, but absent when tailscaled runs in
# userspace-networking mode (the default inside containers) — don't hardcode it.
TS_IFACE="$(ip -o link show 2>/dev/null | grep -oE 'tailscale[0-9]+' | head -n1 || true)"

# Container? Under Docker host-networking the port binds the *host's* stack, and
# the firewall belongs on the host, not in this namespace.
IN_CONTAINER=0
if [ -f /.dockerenv ] || grep -qaE 'docker|containerd|kubepods' /proc/1/cgroup 2>/dev/null; then
  IN_CONTAINER=1
fi

# A routable public IPv4 means $PORT is reachable from the internet unless
# firewalled. Excludes loopback, link-local, RFC1918 and CGNAT/Tailscale
# (100.64.0.0/10). Empty when the box is purely on private/tailnet addresses.
PUBLIC_IP="$(ip -o -4 addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 \
  | grep -vE '^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.)' \
  | head -n1 || true)"

# ---- remote server: restrict the port to the Tailscale network -----------
# On a public-IP box, port 53317 would otherwise be reachable from the internet
# (the receiver binds all interfaces). Lock it to the Tailscale interface so it
# only answers over the tailnet. Multicast LAN discovery is link-local and never
# routes off-LAN, so nothing else needs opening. Inside a container the firewall
# can't be applied from here — userspace-networking has no tailscale0 and host-
# networking puts the bind on the host's stack — so we detect that and say so.
if [ "$MODE" = "remote" ]; then
  echo "==> Remote server — restricting port $PORT to the Tailscale network."

  if [ "$IN_CONTAINER" = "1" ] && [ -z "$TS_IFACE" ]; then
    # Container + userspace-networking Tailscale: no tailscale0, and typically no
    # CAP_NET_ADMIN to manage netfilter. A firewall can't be applied from in here.
    echo "    Detected: inside a container with userspace-networking Tailscale"
    echo "    (no tailscale0 interface). The receiver binds all interfaces — and under"
    echo "    Docker host-networking that includes the host's PUBLIC interface."
    echo
    echo "    A firewall CANNOT be applied from in here. Apply it on the HOST:"
    echo "      • if the host already default-denies inbound (e.g. only 22/80/443 open),"
    echo "        $PORT is already blocked from the internet yet still reachable over the"
    echo "        tailnet (tailscaled delivers it via loopback) — nothing more to do."
    echo "      • otherwise, on the host run:  ufw deny $PORT"
    echo "    Strongly recommended in this setup: also set a PIN (--pin <code>)."
  elif [ -n "$TS_IFACE" ] && command -v ufw >/dev/null 2>&1; then
    SUDO=""
    [ "$(id -u)" -ne 0 ] && SUDO="sudo"
    echo "    Tailscale interface: $TS_IFACE"
    echo "    Applying firewall rules (may prompt for sudo):"
    echo "      ${SUDO:+$SUDO }ufw allow in on $TS_IFACE to any port $PORT"
    echo "      ${SUDO:+$SUDO }ufw deny $PORT"
    if $SUDO ufw allow in on "$TS_IFACE" to any port "$PORT" >/dev/null 2>&1 &&
      $SUDO ufw deny "$PORT" >/dev/null 2>&1; then
      echo "    Done — $PORT answers over Tailscale only."
    else
      echo "    Could not apply automatically (need root/sudo). Run the two commands above yourself."
    fi
  else
    if [ -z "$TS_IFACE" ]; then
      echo "    NOTE: no tailscale interface found. If tailscale isn't up yet, install it"
      echo "          and run 'tailscale up', then re-run this installer. If it's running"
      echo "          in userspace-networking mode, firewall the port on the host instead."
      TS_IFACE="tailscale0"
    fi
    echo "    ufw not found. Apply the equivalent in your firewall:"
    echo "      • allow inbound TCP $PORT only on the '$TS_IFACE' interface"
    echo "      • deny inbound $PORT on all other interfaces"
    echo "    nftables example (inet filter, input chain):"
    echo "      iifname \"$TS_IFACE\" tcp dport $PORT accept"
    echo "      tcp dport $PORT drop"
  fi
  echo "    Tip: a PIN adds a second layer — run with --pin <code> (or set it in Settings)."
fi

# ---- local mode on a public-IP box: inform, don't touch the firewall -----
# We never change the firewall outside remote mode, but a public IP means the
# port is internet-exposed while the TUI is open — so surface it with the exact
# commands to lock it down. (Covers the silent `curl | bash` default-to-local
# case, where the interactive remote prompt never ran.)
if [ "$MODE" != "remote" ] && [ -n "$PUBLIC_IP" ]; then
  iface="${TS_IFACE:-tailscale0}"
  echo
  echo "⚠  Heads up: this machine has a public IP ($PUBLIC_IP) and was installed in"
  echo "   LOCAL mode, so port $PORT was NOT firewalled. The receiver binds all"
  echo "   interfaces, so $PORT is reachable from the internet while the TUI is open."
  echo "   The installer won't change your firewall without remote mode — lock it to"
  echo "   your tailnet yourself (recommended):"
  if [ "$IN_CONTAINER" = "1" ]; then
    echo "     • You're in a container — apply on the HOST, not in here:  ufw deny $PORT"
    echo "       (if the host already default-denies inbound, $PORT is already blocked"
    echo "        from the internet yet still reachable over the tailnet via loopback)."
  elif command -v ufw >/dev/null 2>&1; then
    echo "     • ufw allow in on $iface to any port $PORT"
    echo "     • ufw deny $PORT"
  else
    echo "     • nftables (inet filter, input chain):"
    echo "         iifname \"$iface\" tcp dport $PORT accept"
    echo "         tcp dport $PORT drop"
  fi
  echo "   Or re-run to firewall it automatically:  OMARCHY_SEND_MODE=remote bash install.sh"
  echo "   And/or set a PIN:  omarchy-send --pin <code>"
  echo "   Verify with an app-layer probe (raw TCP/nc lie behind some providers):"
  echo "     curl -sk https://<public-ip>:$PORT/api/localsend/v2/info   # should time out"
fi

# ---- userspace-networking Tailscale: outbound proxy check -----------------
# Under tailscaled --tun=userspace-networking (no TUN device — the default in
# unprivileged containers), processes cannot dial OUT to tailnet addresses at
# all; tailscaled's SOCKS5 proxy is the only outbound path. omarchy-send
# auto-detects and uses it at localhost:1055 — so check it's there and say
# exactly what to do when it isn't (receive works either way; send doesn't).
if command -v tailscale >/dev/null 2>&1 && [ -z "$TS_IFACE" ] \
  && tailscale status >/dev/null 2>&1; then
  if (exec 3<>/dev/tcp/127.0.0.1/1055) 2>/dev/null; then
    exec 3>&- || true
    echo
    echo "==> Userspace-networking Tailscale detected; SOCKS5 proxy found at"
    echo "    localhost:1055 — sends to tailnet devices will route through it"
    echo "    automatically. Nothing to do."
  else
    echo
    echo "⚠  Tailscale is running in userspace-networking mode (no TUN interface)"
    echo "   and no SOCKS5 proxy is listening on localhost:1055. This box can"
    echo "   RECEIVE over the tailnet, but CANNOT SEND to tailnet devices until"
    echo "   tailscaled is restarted with its proxy enabled — add this flag to"
    echo "   however tailscaled is launched (entrypoint, unit, …), keeping the"
    echo "   existing --state/--socket flags:"
    echo
    echo "       tailscaled --tun=userspace-networking --socks5-server=localhost:1055 …"
    echo
    echo "   omarchy-send then uses the proxy automatically — no env vars needed."
    echo "   (Explicit HTTPS_PROXY/HTTP_PROXY/NO_PROXY are honoured as overrides.)"
  fi
fi

echo
case ":$PATH:" in
  *":$BIN_DIR:"*) : ;;
  *) echo "Note: $BIN_DIR is not on your PATH. Add it, or run $BIN directly." ;;
esac
echo "Done. Run: omarchy-send"
