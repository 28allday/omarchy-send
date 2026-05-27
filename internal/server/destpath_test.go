package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDestPathPreservesSubdirs(t *testing.T) {
	dir := t.TempDir()
	got, err := destPath(dir, "Trip/day1/img.jpg")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "Trip", "day1", "img.jpg")
	if got != want {
		t.Fatalf("destPath = %q, want %q", got, want)
	}
	// Parent directories must already exist so the writer can create the file.
	if _, err := os.Stat(filepath.Dir(got)); err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}
}

// Traversal attempts must be neutralised — the result always stays under dir.
func TestDestPathRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"../escape.txt",
		"../../etc/passwd",
		"a/../../../oops.txt",
		"/abs/path.txt",
	} {
		got, err := destPath(dir, name)
		if err != nil {
			continue // rejected outright — also acceptable
		}
		if got != dir && !strings.HasPrefix(got, dir+string(os.PathSeparator)) {
			t.Fatalf("destPath(%q) = %q escaped %q", name, got, dir)
		}
	}
}

// A name colliding with an existing file gets a " (n)" suffix; subdir collisions
// are resolved within their own directory.
func TestDestPathDeDup(t *testing.T) {
	dir := t.TempDir()
	first, _ := destPath(dir, "sub/file.txt")
	if err := os.WriteFile(first, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := destPath(dir, "sub/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatalf("expected a de-duplicated path, got %q twice", second)
	}
	want := filepath.Join(dir, "sub", "file (1).txt")
	if second != want {
		t.Fatalf("dedup = %q, want %q", second, want)
	}
}
