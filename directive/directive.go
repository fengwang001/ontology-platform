// Package directive parses Cache-Control style directive text: comma
// separated "name" or "name=delta" tokens. Names are case insensitive and
// returned lower-cased; delta must be a non-negative decimal integer,
// optionally double-quoted; invalid tokens are skipped individually; an
// overflowing delta is clamped to math.MaxInt64; first occurrence wins.
package directive

import (
	"math"
	"strconv"
	"strings"
)

// MaxStaleAny is sentinel delta for a bare "max-stale": any staleness accepted.
const MaxStaleAny int64 = -1

// Set is the parsed result of one Cache-Control header value.
type Set struct {
	first  []string
	delta  map[string]int64
	exists map[string]bool
	bare   map[string]bool // present without an "=" argument
}

// Parse parses a Cache-Control header value.
func Parse(text string) Set {
	s := Set{
		delta:  map[string]int64{},
		exists: map[string]bool{},
		bare:   map[string]bool{},
	}
	for _, item := range strings.Split(text, ",") {
		name, raw, hasEq := splitToken(item)
		if name == "" || s.exists[name] {
			continue
		}
		if requiresDelta(name) {
			if !hasEq {
				if name == "max-stale" {
					s.remember(name, MaxStaleAny)
					s.bare[name] = true
				}
				continue
			}
			n, ok := parseDelta(raw)
			if !ok {
				continue
			}
			s.remember(name, n)
			continue
		}
		// Token directives: any "=..." argument is ignored, presence kept.
		s.remember(name, 0)
		if !hasEq {
			s.bare[name] = true
		}
	}
	return s
}

func (s Set) remember(name string, delta int64) {
	s.exists[name] = true
	s.first = append(s.first, name)
	s.delta[name] = delta
}

// Has reports whether name is present.
func (s Set) Has(name string) bool { return s.exists[strings.ToLower(name)] }

// Bare reports whether name appeared without an "=" argument.
func (s Set) Bare(name string) bool { return s.bare[strings.ToLower(name)] }

// Get returns the delta-seconds of name and true. Bare max-stale yields
// MaxStaleAny. Missing directives yield (0, false).
func (s Set) Get(name string) (int64, bool) {
	name = strings.ToLower(name)
	if !s.exists[name] {
		return 0, false
	}
	return s.delta[name], true
}

// First returns parsed directive names in first-occurrence order.
func (s Set) First() []string { return append([]string(nil), s.first...) }

func splitToken(item string) (name, raw string, hasEq bool) {
	name, raw, hasEq = strings.Cut(strings.TrimSpace(item), "=")
	name = strings.ToLower(strings.TrimSpace(name))
	if hasEq {
		raw = strings.TrimSpace(raw)
	}
	return
}

func requiresDelta(name string) bool {
	switch name {
	case "max-age", "s-maxage", "min-fresh", "max-stale":
		return true
	}
	return false
}

func parseDelta(raw string) (int64, bool) {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	if raw == "" || raw[0] == '-' || raw[0] == '+' {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		// Only overflow is tolerated: treat as a very large value.
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return math.MaxInt64, true
		}
		return 0, false
	}
	return n, true
}
