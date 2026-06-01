// Command omarchy-send is a LocalSend-compatible file-transfer client with a terminal
// UI, designed to run headless over SSH on Arch/Omarchy servers.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"omarchy-send/internal/app"
	"omarchy-send/internal/client"
	"omarchy-send/internal/config"
	"omarchy-send/internal/dbg"
	"omarchy-send/internal/discovery"
	"omarchy-send/internal/notify"
	"omarchy-send/internal/server"
	"omarchy-send/internal/transfer"
	"omarchy-send/internal/tui"
)

// controller adapts the discovery + sender + server services to tui.Controller.
type controller struct {
	disc   *discovery.Discoverer
	sender *client.Sender
	srv    *server.Server
	notify *atomic.Bool // live gate for desktop notifications (toggled from Settings)
}

func (c controller) Announce()                                         { c.disc.Announce() }
func (c controller) Send(p discovery.Peer, paths []string, pin string) { c.sender.Send(p, paths, pin) }
func (c controller) SendMessage(p discovery.Peer, text, pin string) {
	c.sender.SendMessage(p, text, pin)
}

// The Set* receiver/server toggles no-op when there is no server — quick-send
// mode (Nautilus right-click) runs server-less so it can coexist with an
// already-running instance without fighting over the listen port.
func (c controller) SetAutoAccept(v bool) {
	if c.srv != nil {
		c.srv.SetAutoAccept(v)
	}
}
func (c controller) SetPIN(pin string) {
	if c.srv != nil {
		c.srv.SetPIN(pin)
	}
}
func (c controller) SetReceiveDir(dir string) {
	if c.srv != nil {
		c.srv.SetReceiveDir(dir)
	}
}
func (c controller) SetNotify(v bool) { c.notify.Store(v) }

// SetAlias updates the alias across all services and re-announces it.
func (c controller) SetAlias(alias string) {
	c.disc.SetAlias(alias)
	c.srv.SetAlias(alias)
	c.sender.SetAlias(alias)
	c.disc.Announce()
}

func main() {
	var (
		aliasFlag = flag.String("alias", "", "device alias (overrides config for this run)")
		portFlag  = flag.Int("port", 0, "listen port (overrides config for this run)")
		dirFlag   = flag.String("dir", "", "receive directory (overrides config for this run)")
		pinFlag   = flag.String("pin", "", "require this PIN from senders (overrides config)")
		autoFlag  = flag.Bool("auto-accept", false, "auto-accept incoming transfers (no prompt)")
		noIcons   = flag.Bool("no-icons", false, "hide Nerd Font device icons (for non-Nerd-Font terminals)")
		noNotify  = flag.Bool("no-notify", false, "don't raise desktop notifications on incoming messages/files")

		// Headless one-shot send (no TUI): -to <alias> -message <text>.
		toFlag      = flag.String("to", "", "headless send: target peer alias to send to (no TUI); requires -message")
		messageFlag = flag.String("message", "", "headless send: plain-text message to send to -to")
		sendPINFlag = flag.String("send-pin", "", "headless send: PIN to present if the target peer requires one")
		waitFlag    = flag.Duration("wait", 15*time.Second, "headless send: how long to wait for the target peer to be discovered")
	)
	flag.Parse()

	// The TUI owns the terminal, so keep stray stdlib logging (e.g. net/http's
	// "unsolicited response on idle channel" notice) off the screen — route it
	// to the debug log when enabled, otherwise discard it.
	log.SetOutput(dbg.Writer())

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	if *aliasFlag != "" {
		cfg.Alias = *aliasFlag
		cfg.DeviceModel = *aliasFlag
	}
	if *portFlag != 0 {
		cfg.Port = *portFlag
	}
	if *dirFlag != "" {
		cfg.ReceiveDir = *dirFlag
	}
	if *pinFlag != "" {
		cfg.PIN = *pinFlag
	}
	if *autoFlag {
		cfg.AutoAccept = true
	}
	if *noIcons {
		cfg.NoIcons = true
	}
	if *noNotify {
		cfg.NoNotify = true
	}

	// Headless one-shot send: resolve the target by alias over discovery, send,
	// and exit — no TUI, no terminal required. Suitable for scripts and cron.
	if *toFlag != "" || *messageFlag != "" {
		if *toFlag == "" || *messageFlag == "" {
			fmt.Fprintln(os.Stderr, "headless send needs both -to <alias> and -message <text>")
			os.Exit(2)
		}
		os.Exit(runHeadlessSend(cfg, *toFlag, *messageFlag, *sendPINFlag, *waitFlag))
	}

	// Quick-send: any positional arguments are file/folder paths to send (the
	// Nautilus right-click integration calls `omarchy-send <paths…>`). Open the
	// TUI with them pre-staged, on the device list.
	if args := flag.Args(); len(args) > 0 {
		paths := make([]string, 0, len(args))
		for _, a := range args {
			if abs, err := filepath.Abs(a); err == nil {
				paths = append(paths, abs)
			} else {
				paths = append(paths, a)
			}
		}
		os.Exit(runQuickSend(cfg, paths))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	disc := discovery.New(cfg.DeviceInfo())

	var cert *tls.Certificate
	if cfg.Protocol == "https" {
		c, err := cfg.TLSCertificate()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tls: %v\n", err)
			os.Exit(1)
		}
		cert = &c
	}

	srv := server.New(server.Options{
		Info:       cfg.DeviceInfo(),
		OnPeer:     disc.NotePeer,
		Cert:       cert,
		ReceiveDir: cfg.ReceiveDir,
		AutoAccept: cfg.AutoAccept,
		PIN:        cfg.PIN,
	})
	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
	if err := disc.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: %v\n", err)
		os.Exit(1)
	}

	sender := client.New(cfg.DeviceInfo())

	// notifyOn is the live notification gate: the user's preference (-no-notify /
	// the Settings toggle), AND only meaningful where notify-send can actually
	// reach a daemon. notify.Send itself no-ops when unavailable, so the gate
	// only carries the user preference here.
	notifyOn := &atomic.Bool{}
	notifyOn.Store(!cfg.NoNotify)
	ctrl := controller{disc: disc, sender: sender, srv: srv, notify: notifyOn}

	p := tea.NewProgram(tui.New(cfg, ctrl), tea.WithAltScreen())
	app.BridgeDiscovery(ctx, disc.Events(), p.Send)
	notifyFn := func(summary, body string) {
		if notifyOn.Load() {
			notify.Send(summary, body)
		}
	}
	app.BridgeServer(ctx, srv.Accepts(), srv.Transfers(), srv.Messages(), p.Send, notifyFn)
	app.BridgeTransfers(ctx, sender.Events(), p.Send)
	disc.Announce() // announce immediately so we appear without waiting a tick

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		os.Exit(1)
	}
}

// runQuickSend opens the TUI on the device list with paths pre-staged, so the
// user just picks a recipient. It runs server-less (discovery + sender only),
// like runHeadlessSend, so it coexists with an already-running receiver instead
// of crashing on the busy listen port.
func runQuickSend(cfg config.Config, paths []string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	disc := discovery.New(cfg.DeviceInfo())
	if err := disc.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: %v\n", err)
		return 1
	}
	sender := client.New(cfg.DeviceInfo())

	// No receiver in quick-send mode, so nothing to notify about.
	notifyOff := &atomic.Bool{}
	ctrl := controller{disc: disc, sender: sender, srv: nil, notify: notifyOff}

	p := tea.NewProgram(tui.New(cfg, ctrl, tui.WithStagedFiles(paths)), tea.WithAltScreen())
	app.BridgeDiscovery(ctx, disc.Events(), p.Send)
	app.BridgeTransfers(ctx, sender.Events(), p.Send)
	disc.Announce() // solicit replies immediately rather than waiting a tick

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		return 1
	}
	return 0
}

// runHeadlessSend discovers the peer whose alias matches target (case-
// insensitively), sends it a plain-text message, and returns a process exit
// code. It deliberately starts only discovery — not the HTTP receiver — so it
// can run alongside an already-running instance without fighting over the
// listen port. Status goes to stderr; the success line goes to stdout.
func runHeadlessSend(cfg config.Config, target, message, sendPIN string, wait time.Duration) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	disc := discovery.New(cfg.DeviceInfo())
	if err := disc.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "discovery: %v\n", err)
		return 1
	}
	disc.Announce() // solicit replies immediately rather than waiting a tick

	want := strings.TrimSpace(target)
	fmt.Fprintf(os.Stderr, "Looking for %q on the network (up to %s)…\n", want, wait)

	findCtx, findCancel := context.WithTimeout(ctx, wait)
	defer findCancel()
	peer, err := disc.FindPeer(findCtx, func(p discovery.Peer) bool {
		return strings.EqualFold(strings.TrimSpace(p.Info.Alias), want)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "no peer named %q found within %s.\n", want, wait)
		if seen := disc.Snapshot(); len(seen) > 0 {
			fmt.Fprintln(os.Stderr, "Peers seen:")
			for _, p := range seen {
				fmt.Fprintf(os.Stderr, "  - %q (%s)\n", p.Info.Alias, p.IP)
			}
		} else {
			fmt.Fprintln(os.Stderr, "No peers were seen at all — check you're on the same LAN and the target is running omarchy-send / LocalSend.")
		}
		return 1
	}

	sender := client.New(cfg.DeviceInfo())
	if err := sender.SendMessageSync(peer, message, sendPIN); err != nil {
		switch {
		case errors.Is(err, transfer.ErrPinRequired):
			fmt.Fprintf(os.Stderr, "%q requires a PIN — pass it with -send-pin.\n", peer.Info.Alias)
		default:
			fmt.Fprintf(os.Stderr, "send to %q (%s) failed: %v\n", peer.Info.Alias, peer.IP, err)
		}
		return 1
	}

	fmt.Printf("Message sent to %q (%s).\n", peer.Info.Alias, peer.IP)
	return 0
}
