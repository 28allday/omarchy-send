package tui

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// fzfMaxEntries caps how many paths the recursive walk indexes, so the send
// finder stays responsive even when rooted at a huge tree. When the cap is hit
// the header shows a trailing "+".
const fzfMaxEntries = 50000

// fzfSkipDirs are directory names the walk never descends into: build/cache
// noise that would crowd out real files (and balloon the index) when sending.
// Dot-directories are skipped too (see walkIndex), except the root itself.
var fzfSkipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	".cache":       true,
	".svn":         true,
	"__pycache__":  true,
	".venv":        true,
	"vendor":       true,
}

// fzfEntry is one indexed path under the finder's root.
type fzfEntry struct {
	path string // absolute path
	rel  string // path relative to the root — the fuzzy-match corpus + display
	dir  bool
}

// fzfIndexedMsg carries the result of a background walk back to the model. The
// root is echoed so a stale result (from a root the user has since changed) can
// be ignored.
type fzfIndexedMsg struct {
	root    string
	entries []fzfEntry
	trunc   bool
	err     error
}

// indexFiles walks root in the background and returns its files+folders.
func indexFiles(root string) tea.Cmd {
	return func() tea.Msg {
		entries, trunc, err := walkIndex(root)
		return fzfIndexedMsg{root: root, entries: entries, trunc: trunc, err: err}
	}
}

// walkIndex recursively lists files and directories under root, skipping
// dot-directories and the fzfSkipDirs noise, capped at fzfMaxEntries. Unreadable
// directories are skipped rather than aborting the walk.
func walkIndex(root string) ([]fzfEntry, bool, error) {
	root = filepath.Clean(root)
	var entries []fzfEntry
	truncated := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir // unreadable dir — skip it, keep going
			}
			return nil
		}
		if len(entries) >= fzfMaxEntries {
			truncated = true
			return filepath.SkipAll
		}
		if path == root {
			return nil // don't index the root itself
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || fzfSkipDirs[name] {
				return fs.SkipDir
			}
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		entries = append(entries, fzfEntry{path: path, rel: rel, dir: d.IsDir()})
		return nil
	})
	if err != nil {
		return entries, truncated, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	return entries, truncated, nil
}

// recomputeMatches refreshes the ranked match list for the current query and
// snaps the cursor back to the best match. When fzfDirsOnly is set, only
// directories are kept — for quickly picking a whole folder to send.
func (m *Model) recomputeMatches() {
	m.fzfMatches = m.fzfMatches[:0]
	keep := func(i int) bool { return !m.fzfDirsOnly || m.fzfEntries[i].dir }
	if q := strings.TrimSpace(m.fzfQuery.Value()); q == "" {
		for i := range m.fzfEntries {
			if keep(i) {
				m.fzfMatches = append(m.fzfMatches, i)
			}
		}
	} else {
		for _, r := range fuzzy.Find(q, m.fzfRels) {
			if keep(r.Index) {
				m.fzfMatches = append(m.fzfMatches, r.Index)
			}
		}
	}
	m.fzfCursor = 0
}

// toggleStageCursor stages (or unstages) the highlighted entry.
func (m *Model) toggleStageCursor() {
	if m.fzfCursor < 0 || m.fzfCursor >= len(m.fzfMatches) {
		return
	}
	p := m.fzfEntries[m.fzfMatches[m.fzfCursor]].path
	if contains(m.staged, p) {
		out := m.staged[:0]
		for _, q := range m.staged {
			if q != p {
				out = append(out, q)
			}
		}
		m.staged = out
		return
	}
	m.staged = append(m.staged, p)
}

// updateFzf handles keys on the send (fuzzy-finder) screen. Printable keys edit
// the query; navigation and actions use arrows and ctrl-combos so they don't
// collide with typing.
func (m Model) updateFzf(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenPeers
		return m, nil
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "up", "ctrl+p":
		if m.fzfCursor > 0 {
			m.fzfCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.fzfCursor < len(m.fzfMatches)-1 {
			m.fzfCursor++
		}
		return m, nil
	case "enter":
		m.toggleStageCursor()
		return m, nil
	case "ctrl+d":
		m.fzfDirsOnly = !m.fzfDirsOnly
		m.recomputeMatches()
		return m, nil
	case "ctrl+s":
		if len(m.staged) > 0 && m.target != nil && m.ctrl != nil {
			m.sendPeer = m.target
			m.sendPaths = m.staged
			m.pendingMsg = "" // a file send, not a message
			m.ctrl.Send(*m.target, m.staged, "")
			m.staged = nil
			m.screen = screenTransfers
		}
		return m, nil
	case "ctrl+u":
		if parent := filepath.Dir(m.fzfRoot); parent != "" && parent != m.fzfRoot {
			m.fzfRoot = parent
			m.fzfIndexing = true
			m.fzfErr = ""
			m.fzfEntries, m.fzfRels, m.fzfMatches, m.fzfCursor = nil, nil, nil, 0
			return m, indexFiles(parent)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.fzfQuery, cmd = m.fzfQuery.Update(msg)
	m.recomputeMatches()
	return m, cmd
}

// sendView renders the fuzzy-finder send screen: header, query, ranked matches,
// and the staged panel — sized to fill the framed area.
func (m Model) sendView() string {
	target := ""
	if m.target != nil {
		target = m.target.Info.Alias
	}

	status := collapseHome(m.fzfRoot)
	if m.fzfIndexing {
		status += " · indexing…"
	} else {
		status += fmt.Sprintf(" · %d items", len(m.fzfEntries))
		if m.fzfTrunc {
			status += "+"
		}
	}
	if m.fzfDirsOnly {
		status += " · folders only"
	}
	header := titleStyle.Render("Send to "+target) + "  " + headerStyle.Render(status)

	var errLine string
	if m.fzfErr != "" {
		errLine = lipgloss.NewStyle().Foreground(bad).Render("  "+m.fzfErr) + "\n"
	}

	staged := m.stagedPanel()

	_, ih := innerDims(m.width, m.height)
	used := 2 + lipgloss.Height(staged) // header + query rows + staged panel
	if errLine != "" {
		used++
	}
	rows := ih - used
	if rows < 1 {
		rows = 1
	}

	var b strings.Builder
	b.WriteString(header + "\n")
	b.WriteString(m.fzfQuery.View() + "\n")
	b.WriteString(errLine)
	b.WriteString(m.fzfListView(rows) + "\n")
	b.WriteString(staged)
	return b.String()
}

// fzfListView renders exactly rows lines of the match window around the cursor.
func (m Model) fzfListView(rows int) string {
	if rows < 1 {
		rows = 1
	}
	switch {
	case m.fzfIndexing && len(m.fzfEntries) == 0:
		return padLines(headerStyle.Render("  indexing "+collapseHome(m.fzfRoot)+" …"), rows)
	case len(m.fzfMatches) == 0:
		hint := "  no files here"
		switch {
		case strings.TrimSpace(m.fzfQuery.Value()) != "":
			hint = "  no matches"
		case m.fzfDirsOnly:
			hint = "  no folders here"
		}
		return padLines(headerStyle.Render(hint), rows)
	}

	// Scroll so the cursor stays within the visible window.
	start := 0
	if m.fzfCursor >= rows {
		start = m.fzfCursor - rows + 1
	}
	end := start + rows
	if end > len(m.fzfMatches) {
		end = len(m.fzfMatches)
		if start = end - rows; start < 0 {
			start = 0
		}
	}

	dirStyle := lipgloss.NewStyle().Foreground(accent)
	selStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	dotStyle := lipgloss.NewStyle().Foreground(good)

	var b strings.Builder
	for i := start; i < end; i++ {
		e := m.fzfEntries[m.fzfMatches[i]]
		label := collapseHome(e.path)
		if e.dir {
			label += "/"
		}
		cursor := "  "
		if i == m.fzfCursor {
			cursor = selStyle.Render("▌ ")
		}
		dot := " "
		if contains(m.staged, e.path) {
			dot = dotStyle.Render("●")
		}
		switch {
		case i == m.fzfCursor:
			label = selStyle.Render(label)
		case e.dir:
			label = dirStyle.Render(label)
		default:
			label = valueStyle.Render(label)
		}
		if i > start {
			b.WriteByte('\n')
		}
		b.WriteString(cursor + dot + " " + label)
	}
	return padLines(b.String(), rows)
}

// padLines forces s to exactly n lines: truncating extra lines and padding short
// blocks with blanks, so the surrounding layout stays stable.
func padLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
