package fold

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIdempotent(t *testing.T) {
	for _, s := range []string{"STRASSE", "strasse", "straße", "İ", "ß", "Hello, 世界"} {
		if once, twice := String(s), String(String(s)); twice != once {
			t.Errorf("String(String(%q)) = %q, want %q", s, twice, once)
		}
	}
}

func TestSinglePass(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString([]string{"a", "é", "世"}[i%3]) // ASCII、变音符、多字节
		}
		s := b.String()
		String(s)
		if got := lastRunes.Load(); got != int64(n) {
			t.Errorf("n=%d: processed %d runes, want exactly %d", n, got, n)
		}
		if got := utf8.RuneCountInString(String(s)); got != n {
			t.Errorf("n=%d: folded rune count %d, want %d", n, got, n)
		}
	}
}
