// Package etag parses HTTP entity tags and compares them.
package etag

import (
	"errors"
	"strings"
)

// Tag is a parsed entity tag: `W/"x"` (weak) or `"x"` (strong).
type Tag struct {
	Weak  bool
	Value string
}

// ErrSyntax reports a malformed entity tag or tag list.
var ErrSyntax = errors.New("etag: syntax error")

// Parse parses a single entity tag.
func Parse(s string) (Tag, error) {
	s = strings.TrimSpace(s)
	var t Tag
	if strings.HasPrefix(s, `W/`) {
		t.Weak = true
		s = s[2:]
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return Tag{}, ErrSyntax
	}
	t.Value = s[1 : len(s)-1]
	return t, nil
}

// Strong reports byte-for-byte equivalence; a weak tag on either
// side makes the comparison fail.
func Strong(a, b Tag) bool { return !a.Weak && !b.Weak && a.Value == b.Value }

// Weak reports semantic equivalence, ignoring weakness.
func Weak(a, b Tag) bool { return a.Value == b.Value }

// ParseList parses a comma-separated tag list, tolerating folding
// whitespace (obs-fold) between items. An empty list is a syntax error.
func ParseList(s string) ([]Tag, error) {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	parts := strings.Split(s, ",")
	out := make([]Tag, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return nil, ErrSyntax
		}
		t, err := Parse(p)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
