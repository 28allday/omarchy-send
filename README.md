# Omarchy-Send (`omarchy-send`)

A [LocalSend](https://localsend.org)-compatible file-transfer client with a
**terminal UI**, built for headless Arch / Omarchy servers used over SSH — no
desktop environment, no clipboard, no browser. It interoperates with the stock
LocalSend mobile and desktop apps on the same LAN, including their default
**encrypted (HTTPS)** mode.

## Features

- **Discovery** — multicast announce/listen on `224.0.0.167:53317` plus the HTTP
  `/register` handshake, with peer aging.
- **Receive** — incoming files are accepted via a prompt (or auto-accepted) and
  written to the receive directory, with live progress.
- **Send** — pick a peer, stage files with a built-in file picker, and upload
  them with progress, rate and ETA.
- **Manage** — browse the receive folder, mark received files (or whole folders)
  and delete the ones you no longer want, behind a confirmation prompt.
- **HTTPS** — generates a self-signed certificate whose fingerprint matches the
  scheme the official client pins (uppercase-hex SHA-256 of the cert DER), so
  stock encrypted peers talk to it with no configuration.
- **Single static binary**, pure-stdlib protocol layer; only the Charm TUI
  libraries are external dependencies.

## Install

One line, nothing to clone:

```sh
curl -fsSL https://raw.githubusercontent.com/28allday/omarchy-send/main/install.sh | bash
```

This downloads the right binary for your architecture into `~/.local/bin`, and on
Omarchy also adds a floating Walker entry (search **Omarchy-Send**). Override the
location with `BIN_DIR=/usr/local/bin`, or pin a version with
`OMARCHY_SEND_VERSION=v0.1.0`.

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
```

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

- `1`/`2`/`3`/`4` or `tab` — switch between Devices / Transfers / Manage / Settings
- Peers: `enter` send to the selected peer · `r` refresh · `/` filter
- Send picker: `enter` stage a file · `backspace` unstage · `S` send · `esc` back
- Incoming prompt: `y` accept · `n` reject
- Transfers: `c` clear finished
- Manage: `space` mark file/folder · `a` mark all · `d` delete marked (or the one
  under the cursor) · `r` refresh · `/` filter — deletion asks to confirm first
- Settings: `e` edit (alias / receive dir / PIN) · `a` toggle auto-accept
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
