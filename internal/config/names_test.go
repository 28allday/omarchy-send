package config

import (
	"strings"
	"testing"
)

// randomAlias must always be a "Colour Object" pair drawn from the curated,
// copyright-safe word lists.
func TestRandomAliasFormat(t *testing.T) {
	colours := make(map[string]bool, len(aliasColours))
	for _, w := range aliasColours {
		colours[w] = true
	}
	objects := make(map[string]bool, len(aliasObjects))
	for _, w := range aliasObjects {
		objects[w] = true
	}

	for i := 0; i < 500; i++ {
		a := randomAlias()
		parts := strings.SplitN(a, " ", 2)
		if len(parts) != 2 {
			t.Fatalf("alias %q is not two words", a)
		}
		if !colours[parts[0]] {
			t.Fatalf("alias %q: colour %q not in aliasColours", a, parts[0])
		}
		if !objects[parts[1]] {
			t.Fatalf("alias %q: object %q not in aliasObjects", a, parts[1])
		}
	}
}

// The word lists must contain no duplicates, or the real pool is smaller than
// it looks.
func TestAliasWordsUnique(t *testing.T) {
	for _, list := range []struct {
		name  string
		words []string
	}{
		{"aliasColours", aliasColours},
		{"aliasObjects", aliasObjects},
	} {
		seen := make(map[string]bool, len(list.words))
		for _, w := range list.words {
			if seen[w] {
				t.Errorf("%s: duplicate word %q", list.name, w)
			}
			seen[w] = true
		}
	}
}

// Sanity check that the generator actually varies (not stuck on one value).
func TestRandomAliasVaries(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 200; i++ {
		seen[randomAlias()] = struct{}{}
	}
	if len(seen) < 10 {
		t.Fatalf("randomAlias produced only %d distinct values over 200 draws", len(seen))
	}
}
