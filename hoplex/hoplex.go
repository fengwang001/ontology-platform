// Package hoplex parses two kinds of forwarding header text into one
// ordered hop sequence. It must not import netmatch.
package hoplex

import (
	"errors"
	"net"
	"net/netip"
	"strings"
)

const (
	SourceChain = "chain" // comma chain: leftmost is farthest
	SourcePairs = "pairs" // key=value records: leftmost is farthest
)

// Hop is one forwarding record. Present=false means a missing address.
type Hop struct {
	Source  string
	Present bool
	Addr    netip.Addr
}

// ErrBadHopAddress marks a value that is neither an address nor a marker.
var ErrBadHopAddress = errors.New("hoplex: invalid hop address")

type indexError struct{ idx int }

func (e *indexError) Error() string { return ErrBadHopAddress.Error() }
func (e *indexError) Unwrap() error { return ErrBadHopAddress }
func (e *indexError) HopIndex() int { return e.idx }

// HopIndex returns the 1-based hop index carried by err, or 0.
func HopIndex(err error) int {
	var vi interface{ HopIndex() int }
	if errors.As(err, &vi) {
		return vi.HopIndex()
	}
	return 0
}

func splitQuoted(s string, sep byte) []string {
	out := []string{}
	var cur strings.Builder
	q := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			q = !q
			cur.WriteByte(c)
		case c == sep && !q:
			out, cur = append(out, cur.String()), strings.Builder{}
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, cur.String())
}

func parseAddrText(s string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(s); err == nil {
		return a, true
	}
	host := s
	if h, _, err := net.SplitHostPort(s); err == nil {
		host = h
	}
	a, err := netip.ParseAddr(host)
	return a, err == nil
}

func parse(src, text string, pairs bool) ([]Hop, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var out []Hop
	for i, rec := range splitQuoted(text, ',') {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		if !pairs {
			a, ok := parseAddrText(strings.TrimSpace(rec))
			if !ok {
				return nil, &indexError{i + 1}
			}
			out = append(out, Hop{Source: src, Present: true, Addr: a})
			continue
		}
		val, found := "", false
		for _, item := range splitQuoted(rec, ';') {
			k, v, ok := strings.Cut(item, "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "for") {
				val, found = strings.TrimSpace(v), true
			}
		}
		if found && len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		if !found || val == "" || val == "unknown" || strings.HasPrefix(val, "_") {
			out = append(out, Hop{Source: src})
			continue
		}
		a, ok := parseAddrText(val)
		if !ok {
			return nil, &indexError{i + 1}
		}
		out = append(out, Hop{Source: src, Present: true, Addr: a})
	}
	return out, nil
}

// ParseChain parses "a, b, c"; leftmost is the farthest hop.
func ParseChain(text string) ([]Hop, error) {
	return parse(SourceChain, text, false)
}

// ParsePairs parses "for=a;proto=b, for=c"; only for is used.
func ParsePairs(text string) ([]Hop, error) {
	return parse(SourcePairs, text, true)
}
