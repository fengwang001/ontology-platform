// Package route implements path normalization and segment-boundary
// longest-prefix matching for the gateway router.
package route

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidPath is returned by Normalize for paths that are not absolute,
// contain forbidden bytes, or escape the root via "..".
var ErrInvalidPath = errors.New("route: invalid path")

// Normalize canonicalizes p: it must start with "/", must not contain
// '%', '?', '#' or bytes < 0x20 / 0x7F (conservatively rejected, no
// percent-decoding). Segments are split on "/"; empty and "." segments are
// dropped, ".." pops the previous segment (popping an empty stack escapes
// the root and is invalid). The result is "/" plus segments joined by "/",
// with no trailing slash.
func Normalize(p string) (string, error) {
	if p == "" || p[0] != '/' {
		return "", ErrInvalidPath
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 0x20 || c == 0x7f || c == '%' || c == '?' || c == '#' {
			return "", ErrInvalidPath
		}
	}
	var segs []string
	for _, s := range strings.Split(p, "/") {
		switch s {
		case "", ".":
		case "..":
			if len(segs) == 0 {
				return "", ErrInvalidPath
			}
			segs = segs[:len(segs)-1]
		default:
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(segs, "/"), nil
}

// IsNormalized reports whether p is valid and already in canonical form.
func IsNormalized(p string) bool {
	n, err := Normalize(p)
	return err == nil && n == p
}

// segments returns the canonical segments of a normalized path.
func segments(p string) []string {
	if p == "/" {
		return nil
	}
	return strings.Split(p[1:], "/")
}

type node[T any] struct {
	children map[string]*node[T]
	val      T
	hasVal   bool
}

// Matcher is a segment trie mapping normalized path prefixes to values.
// Longest-prefix lookups examine at most len(segments(path))+1 nodes,
// independent of the number of entries.
type Matcher[T any] struct {
	root *node[T]
	size int
}

// NewMatcher returns an empty Matcher.
func NewMatcher[T any]() *Matcher[T] {
	return &Matcher[T]{root: &node[T]{}}
}

// Add inserts prefix -> val. prefix must be normalized. It returns false
// if prefix is already present (nothing is changed).
func (m *Matcher[T]) Add(prefix string, val T) bool {
	n := m.root
	for _, s := range segments(prefix) {
		c, ok := n.children[s]
		if !ok {
			c = &node[T]{}
			if n.children == nil {
				n.children = make(map[string]*node[T])
			}
			n.children[s] = c
		}
		n = c
	}
	if n.hasVal {
		return false
	}
	n.val, n.hasVal = val, true
	m.size++
	return true
}

// Size returns the number of entries.
func (m *Matcher[T]) Size() int { return m.size }

// lookup finds the longest segment-boundary prefix of p. p must be
// normalized. It reports the matched value, the matched prefix length in
// bytes, and the number of trie nodes examined (probes).
func (m *Matcher[T]) lookup(p string) (val T, prefixLen int, ok bool, probes int) {
	n := m.root
	probes++
	best := -1
	if n.hasVal {
		val, best = n.val, 1 // prefix "/"
	}
	off := 1
	for _, s := range segments(p) {
		c, hit := n.children[s]
		if !hit {
			break
		}
		n = c
		probes++
		off += len(s) + 1
		if n.hasVal {
			val, best = n.val, off-1
		}
	}
	if best < 0 {
		return val, 0, false, probes
	}
	return val, best, true, probes
}

// LongestPrefix returns the value at the longest segment-boundary prefix p
// of x (x == p, or x starts with p+"/"; "/" matches everything), together
// with the rest of x after removing p: "" when x == p, x itself when
// p == "/", otherwise x[len(p):] which starts with "/".
func (m *Matcher[T]) LongestPrefix(x string) (val T, rest string, ok bool) {
	v, plen, found, _ := m.lookup(x)
	if !found {
		return v, "", false
	}
	switch {
	case plen == len(x):
		return v, "", true
	case plen == 1: // matched "/"
		return v, x, true
	default:
		return v, x[plen:], true
	}
}

// Entry is one route: a normalized prefix, the scope it requires
// ("" means public), and a non-empty backend name.
type Entry struct {
	Prefix  string
	Scope   string
	Backend string
}

// Table is an immutable longest-prefix routing table.
type Table struct {
	m *Matcher[Entry]
}

// NewTable validates entries (normalized and distinct prefixes, non-empty
// backend) and builds a Table. The first violation only is reported.
func NewTable(entries []Entry) (*Table, error) {
	m := NewMatcher[Entry]()
	for i, e := range entries {
		if !IsNormalized(e.Prefix) {
			return nil, fmt.Errorf("route: entry %d: prefix %q is not normalized", i, e.Prefix)
		}
		if e.Backend == "" {
			return nil, fmt.Errorf("route: entry %d: empty backend name", i)
		}
		if !m.Add(e.Prefix, e) {
			return nil, fmt.Errorf("route: entry %d: duplicate prefix %q", i, e.Prefix)
		}
	}
	return &Table{m: m}, nil
}

// LongestPrefix finds the route for the normalized path p.
func (t *Table) LongestPrefix(p string) (Entry, bool) {
	e, _, ok := t.m.LongestPrefix(p)
	return e, ok
}

// Size returns the number of routes.
func (t *Table) Size() int { return t.m.Size() }
