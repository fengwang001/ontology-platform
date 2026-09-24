// Package hoplex parses the two forwarding header syntaxes into a
// uniform hop sequence. Within each header the leftmost record is the
// farthest hop (closest to the real client). Address text is kept raw;
// validating it is the caller's job.
package hoplex

import "strings"

// Source marks which header syntax a hop came from.
type Source int

const (
	Chain     Source = iota // comma list, e.g. X-Forwarded-For
	Forwarded               // key=value records, e.g. RFC 7239 Forwarded
)

// Hop is one forwarding record. Addr is empty when Missing is true.
type Hop struct {
	Source  Source
	Addr    string
	Missing bool // for=unknown or for=_<obfuscated>
}

// ParseChain parses a comma-separated address list.
func ParseChain(header string) []Hop {
	var hops []Hop
	for _, part := range strings.Split(header, ",") {
		if part = strings.TrimSpace(part); part != "" {
			hops = append(hops, Hop{Source: Chain, Addr: part})
		}
	}
	return hops
}

// ParseForwarded parses comma-separated key=value records and keeps
// only the "for" item of each record. Values may be quoted; quotes may
// contain semicolons and commas. Keys are case-insensitive.
func ParseForwarded(header string) []Hop {
	var hops []Hop
	for _, rec := range splitQuoted(header, ',') {
		for _, kv := range splitQuoted(rec, ';') {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(k), "for") {
				continue
			}
			v = strings.TrimSpace(v)
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = v[1 : len(v)-1]
			}
			h := Hop{Source: Forwarded, Addr: v}
			if v == "unknown" || strings.HasPrefix(v, "_") {
				h.Addr, h.Missing = "", true
			}
			hops = append(hops, h)
			break
		}
	}
	return hops
}

// splitQuoted splits s on sep, ignoring separators inside double quotes.
func splitQuoted(s string, sep byte) []string {
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quoted = !quoted
		case sep:
			if !quoted {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}
