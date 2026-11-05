package ids

import (
	"testing"
	"time"
)

func TestNewIsUniqueSortableAndWellFormed(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for i := range 2000 {
		id := New()
		if len(id) != 26 {
			t.Fatalf("len %d: %q", len(id), id)
		}
		for _, c := range id {
			if !containsRune(alphabet, c) {
				t.Fatalf("bad char %q in %q", c, id)
			}
		}
		if seen[id] {
			t.Fatalf("duplicate %q", id)
		}
		seen[id] = true
		if i%500 == 0 {
			time.Sleep(2 * time.Millisecond)
			if prev != "" && id[:10] <= prev[:10] {
				t.Fatalf("time prefix not increasing: %q then %q", prev, id)
			}
			prev = id
		}
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
