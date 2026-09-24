// Package gtid parses and manipulates GTID sets: maps of source UUID to
// normalized transaction-number interval sets.
package gtid

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"ontology/ivl"
)

var (
	ErrSyntax  = errGTID("gtid: syntax error")
	ErrUUID    = errGTID("gtid: invalid UUID")
	ErrRange   = errGTID("gtid: value out of range")
	ErrTooMany = errGTID("gtid: too many intervals")
)

type errGTID string

func (e errGTID) Error() string { return string(e) }

type Set struct{ m map[string]ivl.Set }

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Parse validates text; first violation wins, so rejected text never partially applies.
func Parse(text string) (Set, error) {
	if text == "" {
		return Set{m: map[string]ivl.Set{}}, nil
	}
	if strings.IndexFunc(text, unicode.IsSpace) >= 0 {
		return Set{}, ErrSyntax
	}
	raw := map[string][]ivl.Interval{}
	for _, e := range strings.Split(text, ",") {
		up, rp, ok := strings.Cut(e, ":")
		if !ok || rp == "" {
			return Set{}, ErrSyntax
		}
		u, err := canonUUID(up)
		if err != nil {
			return Set{}, err
		}
		for _, r := range strings.Split(rp, ":") {
			lo, hi, err := parseRange(r)
			if err != nil {
				return Set{}, err
			}
			raw[u] = append(raw[u], ivl.Interval{Lo: lo, Hi: hi})
		}
	}
	out := Set{m: make(map[string]ivl.Set, len(raw))}
	for u, iv := range raw {
		out.m[u] = ivl.NewSet(iv)
	}
	return out, nil
}

func canonUUID(p string) (string, error) {
	if !uuidRE.MatchString(p) {
		return "", ErrUUID
	}
	return strings.ToLower(p), nil
}

func parseRange(r string) (lo, hi int64, err error) {
	p := strings.Split(r, "-")
	if r == "" || len(p) > 2 || p[0] == "" { // "", "5-", "-5", "1-2-3"
		return 0, 0, ErrSyntax
	}
	if lo, err = parseNum(p[0]); err != nil {
		return
	}
	if len(p) == 1 {
		return lo, lo, nil
	}
	if hi, err = parseNum(p[1]); err != nil {
		return
	}
	if lo > hi {
		return 0, 0, ErrRange
	}
	return
}
func parseNum(p string) (int64, error) {
	if p == "" || (len(p) > 1 && p[0] == '0') { // leading zero: "07", "00"
		return 0, ErrSyntax
	}
	n, err := strconv.ParseInt(p, 10, 64) // non-digits and overflow
	if err != nil || n == 0 {
		return 0, ErrRange
	}
	return n, nil
}
func (s Set) Union(o Set) Set {
	out := Set{m: map[string]ivl.Set{}}
	for k, v := range s.m {
		out.m[k] = v
	}
	for k, v := range o.m {
		out.m[k] = out.m[k].Union(v)
	}
	return out
}

// Subtract returns source minus s; it never mutates s.
func (s Set) Subtract(source Set) Set {
	out := Set{m: map[string]ivl.Set{}}
	for k, src := range source.m {
		ec := s.m[k] // local copy: probe tally lands on the copy
		if rem := (&ec).Subtract(src); rem.Len() > 0 {
			out.m[k] = rem
		}
	}
	return out
}

func (s Set) Intervals() (n int) {
	for _, v := range s.m {
		n += v.Len()
	}
	return
}

// String is the canonical text form ("" for the empty set).
func (s Set) String() string {
	keys := make([]string, 0, len(s.m))
	for k, v := range s.m {
		if v.Len() > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		if f := s.m[k].Format(); f != "" {
			b.WriteByte(':')
			b.WriteString(f)
		}
	}
	return b.String()
}
