# Omarchy-Send (`omarchy-send`)

A [LocalSend](https://localsend.org)-compatible file-transfer client with a
**terminal UI**, built for headless Arch / Omarchy servers used over SSH — no
desktop environment, no clipboard, no browser. It interoperates with the stock
LocalSend mobile and desktop apps on the same LAN, including their default
**encrypted (HTTPS)** mode.

## Features

- **Discovery** — multicast announce/listen on `224.0.0.167:53317` plus the HTTP
  `/register` handshake, with peer aging.
- **Remote devices** — reach boxes that aren't on your LAN (multicast can't find
  them) by probing them directly over unicast. If [Tailscale](https://tailscale.com)
  is running, online tailnet peers are discovered automatically; you can also add
  a device by host/IP/name with the `+` key, saved for next time.
- **Receive** — incoming files are accepted via a prompt (or auto-accepted) and
  written to the receive directory, with live progress.
- **Send** — pick a peer, then find what to send with a built-in **recursive
  fuzzy finder**: type part of a name to match files (and folders) anywhere under
  your home directory, stage them, and upload with progress, rate and ETA.
  Folders are sent recursively with their structure preserved on the receiver.
- **Right-click in Nautilus** (Omarchy desktop) — "Send via Omarchy-Send" on any
  file or folder opens the picker in a floating terminal with your selection
  already staged; just pick a device. Installed only where Nautilus is present.
- **Messages** — send a plain-text message to a peer (LocalSend-compatible) and
  read messages others send you in a dedicated Messages tab. Send the system
  clipboard as a message, or copy a received one back to the clipboard (uses
  `wl-clipboard`/`xclip`/`xsel`, or tmux's paste buffer when run inside tmux on
  a headless box).
- **Desktop notifications** — on a graphical session (e.g. Omarchy/Hyprland with
  `mako`), an incoming message or file offer raises a desktop notification via
  `notify-send`, so a backgrounded receiver still gets your attention. Best-effort
  and self-disabling on headless boxes; turn it off with `--no-notify` or the `n`
  key in Settings.
- **Manage** — browse the receive folder, mark received files (or whole folders)
  and delete the ones you no longer want, behind a confirmation prompt.
- **HTTPS** — generates a self-signed certificate whose fingerprint matches the
  scheme the official client pins (uppercase-hex SHA-256 of the cert DER), so
  stock encrypted peers talk to it with no configuration.
- **Single static binary**, pure-stdlib protocol layer; the only external
  dependencies are the Charm TUI libraries and `sahilm/fuzzy` (the send finder's
  matcher) — both compiled in, so headless boxes need nothing extra.

## Install

One line, nothing to clone:

```sh
curl -fsSL https://raw.githubusercontent.com/28allday/omarchy-send/main/install.sh | bash
```

This downloads the right binary for your architecture into `~/.local/bin`, and on
Omarchy also adds a floating Walker entry (search **Omarchy-Send**) and the
Nautilus right-click integration. Override the location with `BIN_DIR=/usr/local/bin`,
or pin a version with `OMARCHY_SEND_VERSION=v0.1.0`.

The installer also drops an **agent context file** so any AI agent on the machine
(Claude, etc.) knows what omarchy-send is and where received files land: a canonical
`~/.config/omarchy-send/AGENTS.md` (with a `CLAUDE.md` symlink) plus a short,
idempotently-managed section appended to `~/.claude/CLAUDE.md`.

**Local or remote?** When run interactively the installer asks whether this is a
**local** machine (home/LAN) or a **remote server** (public IP). Local installs as
above. For a remote server it additionally locks port `53317` to the Tailscale
interface in the firewall (`ufw`), so the box is reachable over your tailnet only —
not the open internet. Non-interactive installs (e.g. piped `curl | bash`) default
to local; force a choice with `OMARCHY_SEND_MODE=local` or `OMARCHY_SEND_MODE=remote`.

If you install in local mode but the box has a **public IP**, the installer detects
it and prints a warning with the exact commands to lock the port down — it never
changes your firewall without remote mode. See
[Public-IP boxes](#public-ip-boxes-firewall-the-port) below.

> The installer is a short shell script fetched over HTTPS; read it first if you
> prefer — it lives at [`install.sh`](install.sh) in this repo.

## Build from source

```sh
git clone https://github.com/28allday/omarchy-send
cd omarchy-send
go build -o omarchy-send ./cmd/omarchy-send   # or: ./install.sh
```

Run from a clone, `./install.sh` builds with your local Go toolchain instead of
downloading.

## Usage

```sh
omarchy-send                      # uses config / sensible defaults
omarchy-send --alias my-server    # override the advertised device name
omarchy-send --port 53317         # override the listen port
omarchy-send --dir ~/Downloads    # override the receive directory
omarchy-send --auto-accept        # accept incoming transfers without a prompt
omarchy-send --pin 2468           # require senders to supply this PIN
omarchy-send --no-icons           # drop Nerd Font glyphs (non-Nerd-Font terminals)
omarchy-send --no-notify          # don't raise desktop notifications on incoming
```

### Headless send (no TUI)

Send a one-off message to a peer by name, with no terminal UI — handy from
scripts, cron, or an SSH session with no TTY:

```sh
omarchy-send --to "Strong Onion" --message "hello"
omarchy-send --to "Strong Onion" --message "deploy finished" --wait 20s
omarchy-send --to "Strong Onion" --message "hi" --send-pin 2468   # if the peer requires a PIN
```

The target is matched against the peer's display name, case-insensitively. The
command discovers the peer over multicast (waiting up to `--wait`, default 15s),
sends the message, prints a one-line result, and exits non-zero if the peer
isn't found or the send fails. It starts discovery only — not the receiver — so
it's safe to run while another `omarchy-send` instance is up. Both `--to` and
`--message` are required; file sending stays in the TUI for now.

### Sending files

Select a device on the **Devices** tab and press `enter` to open the send
finder. It indexes files and folders under your home directory and fuzzy-matches
as you type, so you can jump straight to what you want instead of browsing
folder by folder:

- type to filter · `↑`/`↓` move · `enter` stage the highlighted file **or folder**
- `ctrl+d` show folders only (to send a whole directory) · `ctrl+s` send · `ctrl+u`
  move the search root up a level · `esc` back

Staging a folder sends it whole (its structure is recreated on the receiver).
Matching is case-insensitive, and noisy directories (`.git`, `node_modules`,
caches, dotfiles…) are skipped to keep the index fast.

### Remote devices (over Tailscale)

Multicast discovery only finds peers on the same LAN. To send to / receive from a
box elsewhere, omarchy-send probes it directly over unicast — which works over
anything routable, [Tailscale](https://tailscale.com) being the easy, secure choice
(stable addresses, end-to-end encryption, no port-forwarding):

1. `tailscale up` on both devices (one-time).
2. Either let omarchy-send **auto-discover** online tailnet peers (it probes them
   every few seconds; any running omarchy-send/LocalSend appears in Devices), or
   press **`+`** on the Devices tab and enter a host, IP, or Tailscale name (e.g.
   `colossus`). Added devices are saved to `knownPeers` in the config and re-probed
   on every launch.

The receiver already listens on all interfaces, so it's reachable at its Tailscale
IP with nothing else to configure. Sending and receiving both work, because the
probe is a two-way handshake (each side learns the other).

#### Public-IP boxes: firewall the port

The receiver binds **all interfaces**, so on a box with a public IP, port `53317`
is reachable from the open internet while the TUI is running. Don't leave it that
way. Three ways to handle it:

- **Easiest:** install in **remote** mode — `OMARCHY_SEND_MODE=remote bash install.sh`
  — and the installer applies the `ufw` rules for you (when a real `tailscale0`
  interface is present).
- **Manually**, restrict the port to the tailnet:

  ```sh
  ufw allow in on tailscale0 to any port 53317   # tailnet only
  ufw deny 53317                                 # everything else
  ```

  Or the `nftables` equivalent (inet filter, input chain):

  ```
  iifname "tailscale0" tcp dport 53317 accept
  tcp dport 53317 drop
  ```

- **Inside a container** (e.g. Docker `--network host` with userspace-networking
  Tailscale, where there's no `tailscale0` and no `CAP_NET_ADMIN`): you can't
  firewall from in there — apply it on the **host**. If the host already
  default-denies inbound (only opens e.g. 22/80/443), `53317` is already blocked
  from the internet yet still reachable over the tailnet (tailscaled delivers it
  via loopback) — nothing more to do.

Always set a **`--pin`** as a second layer regardless.

> **Verifying** the port is closed: don't trust `nc -z`, `telnet`, or
> `/dev/tcp` — some hosting providers (Hostinger, DigitalOcean, …) answer the TCP
> handshake (SYN/ACK) for *every* port at their network edge, so those tools
> report a firewalled port as "open". Only an **app-layer** probe is truthful:
>
> ```sh
> curl -sk https://<public-ip>:53317/api/localsend/v2/info   # should time out / hang
> curl -sk https://<tailnet-ip>:53317/api/localsend/v2/info  # returns device info
> ```

### Right-click send (Nautilus)

On an Omarchy desktop, the installer adds a **"Send via Omarchy-Send"** entry to
the Nautilus context menu. Right-click one or more files or folders (multi-select
works) and choose it: a floating terminal opens with your selection pre-staged on
the device list — pick a device and it sends, then the window closes itself once
the transfer finishes.

This is a graphical convenience and is **desktop-only**: it's installed only when
Nautilus is present, so headless servers don't get it (and don't need it — use
the TUI or headless send there). Under the hood it just runs
`omarchy-send <paths…>`, which you can call yourself from any terminal.

### Theming

On Omarchy, the TUI reads the active theme's `~/.config/omarchy/current/theme/colors.toml`
and matches it. Elsewhere (headless / over SSH) it falls back to **ANSI palette
colours**, so it tracks whatever colour scheme the connecting terminal uses.

### Device name

On first run, each device is given a random, friendly display name such as
**"Crimson Quasar"** (a colour + a celestial object), generated once and saved.
This avoids broadcasting your machine's hostname to everyone on the network —
handy on a laptop joining untrusted Wi-Fi. The hostname is still carried in the
device-model field, and you can set any name you like with `--alias` or the `e`
key in the Settings tab.

Config (including the generated TLS identity) is stored at
`~/.config/omarchy-send/config.json`. Received files default to `~/Omarchy-Send/`.

### Unattended / headless mode

For a server that should accept files without anyone at the keyboard, combine
auto-accept with a PIN so only senders who know the code can push to it:

```sh
omarchy-send --auto-accept --pin 2468
```

### Keys

- `1`–`5` or `tab` — switch between Devices / Transfers / Manage / Messages / Settings
- Peers: `enter` send to the selected peer · `m` message · `v` send clipboard · `+` add a remote device · `r` refresh · `/` filter
- PIN-protected peers: messages prompt for the PIN and retry, just like file sends
- Send finder: type to fuzzy-filter · `enter` stage file/folder · `ctrl+d` folders-only · `ctrl+s` send · `ctrl+u` up a dir · `esc` back
- Incoming prompt: `y` accept · `n` reject
- Transfers: `c` clear finished
- Messages: `enter` read the full message · `y` copy it to the clipboard · `d`
  delete it (incoming messages arrive automatically, with a footer notice)
- Manage: `space` mark file/folder · `a` mark all · `d` delete marked (or the one
  under the cursor) · `r` refresh · `/` filter — deletion asks to confirm first
- Settings: `e` edit (alias / receive dir / PIN) · `a` toggle auto-accept · `i` toggle icons · `n` toggle notifications
- Sending to a PIN-protected peer prompts for the PIN and retries
- `q` quit

### Debugging

Set `OMARCHY_SEND_LOG=/path/to/log` to record discovery/transfer events to a file.

## Notes on iOS

iOS LocalSend decides whether received media lands in the Photo Library based on
its own in-app settings; its post-receive "open file" prompt can fail to open
files from the app cache. Both behaviours occur identically with the official
desktop client and are not controlled by the sender.

## License

MIT — see [LICENSE](LICENSE). Omarchy-Send is an independent implementation of
the published [LocalSend protocol](https://github.com/localsend/protocol); it is
not affiliated with the LocalSend project. The terminal UI is built on the
[Charm](https://github.com/charmbracelet) libraries (also MIT).
