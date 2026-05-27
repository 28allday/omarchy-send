// Package client implements the sender side of the LocalSend upload flow:
// prepare-upload to a peer, then stream each file to /upload, emitting progress.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"omarchy-send/internal/dbg"
	"omarchy-send/internal/discovery"
	"omarchy-send/internal/protocol"
	"omarchy-send/internal/transfer"
)

// errOpen wraps a failure to open a source file, so the send loop can skip just
// that file rather than aborting the whole batch (which it does for peer/network
// errors, where the shared session is dead).
var errOpen = errors.New("open source file")

// inflight tracks one running send so it can be cancelled — e.g. when a newer
// transfer to the same peer supersedes it.
type inflight struct {
	cancel context.CancelFunc
}

// Sender uploads files to peers. Events are delivered on Events().
type Sender struct {
	mu     sync.Mutex
	self   protocol.DeviceInfo
	http   *http.Client
	events chan transfer.Event
	active map[string]*inflight // in-flight sends keyed by peer IP
}

// New returns a Sender advertising self. TLS chain validation is disabled (we
// rely on LocalSend's fingerprint model, like the discovery client).
func New(self protocol.DeviceInfo) *Sender {
	return &Sender{
		self: self,
		http: &http.Client{
			// No overall timeout (large files), but bound the parts that can
			// silently wedge on a vanished peer: connecting, the TLS handshake,
			// and waiting for response headers after the body is sent.
			Timeout: 0,
			Transport: &http.Transport{
				TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
		events: make(chan transfer.Event, 256),
		active: make(map[string]*inflight),
	}
}

// Events returns the outgoing-transfer event channel.
func (s *Sender) Events() <-chan transfer.Event { return s.events }

// SetAlias updates the alias we present to peers when sending, at runtime.
func (s *Sender) SetAlias(alias string) {
	s.mu.Lock()
	s.self.Alias = alias
	s.self.DeviceModel = alias
	s.mu.Unlock()
}

func (s *Sender) selfCopy() protocol.DeviceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.self
}

// Send uploads the given file paths to peer in a background goroutine. pin may
// be empty; supply it when the peer requires one.
func (s *Sender) Send(peer discovery.Peer, paths []string, pin string) {
	go s.send(peer, paths, pin)
}

func (s *Sender) send(peer discovery.Peer, paths []string, pin string) {
	// A new transfer to a peer supersedes any still-running one to the same
	// peer: cancel it so a half-finished old batch can't carry on once the user
	// starts something new.
	ctx, cancel := context.WithCancel(context.Background())
	h := &inflight{cancel: cancel}
	s.mu.Lock()
	if prev := s.active[peer.IP]; prev != nil {
		prev.cancel()
	}
	s.active[peer.IP] = h
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		if s.active[peer.IP] == h {
			delete(s.active, peer.IP)
		}
		s.mu.Unlock()
	}()

	// Expand any directories into their files, then build metadata keyed by a
	// generated fileId. A directory's files carry a relative FileName (e.g.
	// "Trip/day1/img.jpg") so the receiver can recreate the folder structure.
	items := s.expand(paths)
	files := make(map[string]protocol.FileMetadata, len(items))
	pathByID := make(map[string]string, len(items))
	for _, it := range items {
		id := randID()
		files[id] = protocol.FileMetadata{
			ID:       id,
			FileName: it.name,
			Size:     it.size,
			FileType: mimeType(it.path),
		}
		pathByID[id] = it.path
	}
	if len(files) == 0 {
		return
	}

	if meta, err := json.Marshal(files); err == nil {
		dbg.Logf("SEND prepare-upload to %s: files=%s", peer.IP, string(meta))
	}
	base := s.url(peer)
	prepResp, err := s.prepareUpload(ctx, base, files, pin)
	if err != nil {
		dbg.Logf("send prepare-upload to %s failed: %v", peer.IP, err)
		if errors.Is(err, transfer.ErrPinRequired) {
			// One signal is enough for the TUI to prompt + retry.
			s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Error, Err: transfer.ErrPinRequired})
			return
		}
		for id, m := range files {
			s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Error, ID: id, FileName: m.FileName, Err: err})
		}
		return
	}

	for id, token := range prepResp.Files {
		meta := files[id]
		key := prepResp.SessionID + ":" + id

		// If the transfer was cancelled (superseded, or aborted after an earlier
		// failure), don't push the rest of the batch — report a clean cancel.
		if ctx.Err() != nil {
			s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Cancel, ID: key, FileName: meta.FileName})
			continue
		}

		err := s.uploadFile(ctx, base, prepResp.SessionID, id, token, key, pathByID[id], meta)
		if err == nil {
			s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.FileDone, ID: key, FileName: meta.FileName, Received: meta.Size, Total: meta.Size})
			continue
		}
		dbg.Logf("send upload %q failed: %v", meta.FileName, err)
		if ctx.Err() != nil {
			s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Cancel, ID: key, FileName: meta.FileName})
		} else {
			s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Error, ID: key, FileName: meta.FileName, Err: err})
		}
		// A failure to open a local file is specific to that file — skip it and
		// keep going. Any other failure means the peer/session is gone, so abort
		// the rest of the batch (the shared session can't be resumed).
		if !errors.Is(err, errOpen) {
			cancel()
		}
	}
}

// fileItem is one concrete file to upload: its path on disk, the relative name
// advertised to the peer (carries folder structure), and its size.
type fileItem struct {
	path string
	name string
	size int64
}

// expand turns the selected paths into a flat list of files. A regular file is
// passed through with its base name. A directory is walked recursively; each
// contained file's advertised name is its path relative to the directory's
// parent, so the selected folder itself is recreated on the receiver (selecting
// "Trip" yields "Trip/day1/img.jpg", …). Unreadable entries are skipped with an
// error event rather than aborting the whole transfer.
func (s *Sender) expand(paths []string) []fileItem {
	var items []fileItem
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Error, FileName: filepath.Base(p), Err: err})
			continue
		}
		if !fi.IsDir() {
			items = append(items, fileItem{path: p, name: filepath.Base(p), size: fi.Size()})
			continue
		}
		root := filepath.Dir(filepath.Clean(p)) // parent, so the folder name is kept
		_ = filepath.WalkDir(p, func(fp string, d fs.DirEntry, err error) error {
			if err != nil {
				s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Error, FileName: filepath.Base(fp), Err: err})
				return nil // skip this entry, keep walking the rest
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Error, FileName: filepath.Base(fp), Err: err})
				return nil
			}
			rel, err := filepath.Rel(root, fp)
			if err != nil {
				rel = filepath.Base(fp)
			}
			items = append(items, fileItem{path: fp, name: filepath.ToSlash(rel), size: info.Size()})
			return nil
		})
	}
	return items
}

func (s *Sender) prepareUpload(ctx context.Context, base string, files map[string]protocol.FileMetadata, pin string) (protocol.PrepareUploadResponse, error) {
	reqBody, _ := json.Marshal(protocol.PrepareUploadRequest{Info: s.selfCopy(), Files: files})
	url := base + protocol.PathPrepareUpload
	if pin != "" {
		url += "?pin=" + neturl.QueryEscape(pin)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return protocol.PrepareUploadResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return protocol.PrepareUploadResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return protocol.PrepareUploadResponse{}, transfer.ErrPinRequired
	}
	if resp.StatusCode != http.StatusOK {
		return protocol.PrepareUploadResponse{}, fmt.Errorf("prepare-upload status %d", resp.StatusCode)
	}
	var pr protocol.PrepareUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return protocol.PrepareUploadResponse{}, err
	}
	return pr, nil
}

func (s *Sender) uploadFile(ctx context.Context, base, sessionID, fileID, token, key, path string, meta protocol.FileMetadata) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: %v", errOpen, err)
	}
	defer f.Close()

	s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Start, ID: key, FileName: meta.FileName, Total: meta.Size})

	pr := &progressReader{
		r:     f,
		total: meta.Size,
		emit: func(sent int64) {
			s.emit(transfer.Event{Dir: transfer.Outgoing, Kind: transfer.Progress, ID: key, FileName: meta.FileName, Received: sent, Total: meta.Size})
		},
	}

	url := fmt.Sprintf("%s%s?sessionId=%s&fileId=%s&token=%s", base, protocol.PathUpload, sessionID, fileID, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = meta.Size

	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload status %d", resp.StatusCode)
	}
	return nil
}

func (s *Sender) url(peer discovery.Peer) string {
	scheme := "https"
	if peer.Info.Protocol == "http" {
		scheme = "http"
	}
	port := peer.Info.Port
	if port == 0 {
		port = protocol.DefaultPort
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(peer.IP, strconv.Itoa(port)))
}

func (s *Sender) emit(ev transfer.Event) {
	select {
	case s.events <- ev:
	default:
	}
}

// builtinMIME maps common extensions to MIME types so a headless server
// without /etc/mime.types still labels photos/videos correctly. (Go's built-in
// table omits .jpg and mislabels .heic as image/heif.)
var builtinMIME = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".tiff": "image/tiff", ".tif": "image/tiff", ".heic": "image/heic",
	".heif": "image/heif", ".dng": "image/x-adobe-dng", ".svg": "image/svg+xml",
	".mp4": "video/mp4", ".mov": "video/quicktime", ".m4v": "video/x-m4v",
	".mkv": "video/x-matroska", ".webm": "video/webm", ".avi": "video/x-msvideo",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".wav": "audio/wav",
	".flac": "audio/flac", ".ogg": "audio/ogg", ".opus": "audio/opus",
	".pdf": "application/pdf", ".zip": "application/zip", ".txt": "text/plain",
}

// mimeType resolves a file's MIME type, preferring our built-in table, then the
// system table, then a safe default.
func mimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if t, ok := builtinMIME[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
