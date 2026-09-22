package main

import (
	"strings"

	"ontology/canon"
	"ontology/pct"
)

func queryModesOK(ord, srt *canon.Normalizer) bool {
	states := []string{"http://h/p?a", "http://h/p?a=", "http://h/p?a=%20"}
	for i := range states {
		for j := range states {
			if mustEq(ord, states[i], states[j]) != (i == j) {
				return false
			}
		}
	}
	if mustEq(ord, "http://h/p?a=1&a=2", "http://h/p?a=2&a=1") {
		return false // ordered: duplicates keep order
	}
	return mustEq(srt, "http://h/p?a=1&a=2", "http://h/p?a=2&a=1")
}

func escapeErrOK(n *canon.Normalizer) bool {
	cases := map[string]pct.Kind{
		"http://h/a%":      pct.KindTruncated,
		"http://h/a%zz":    pct.KindBadHex,
		"http://h/a/%ff":   pct.KindBadUTF8,
		"http://h/p?x=%2":  pct.KindTruncated,
		"http://h/p?x=%g1": pct.KindBadHex,
	}
	for u, kind := range cases {
		r, err := n.Normalize(u)
		pe, ok := err.(*pct.Error)
		if !ok || pe.Kind != kind || pe.Offset < 0 || r.Canonical != "" {
			return false
		}
	}
	return true
}

func equivOK(n *canon.Normalizer) bool {
	groups := [][]string{
		{"http://example.com/a", "HTTP://EXAMPLE.COM.:80/a", "http://example.com/%61"},
		{"http://example.com/a/"},
		{"http://example.com/a%2F"},
		{"https://example.com/a"},
	}
	for gi, g := range groups {
		for _, a := range g {
			for gj, g2 := range groups {
				for _, b := range g2 {
					if mustEq(n, a, b) != (gi == gj) {
						return false
					}
				}
			}
		}
	}
	return true
}

func scanOK() bool {
	n := canon.New(canon.ModeSorted, canon.Limits{
		MaxURLLength: 1 << 20, MaxPathSegments: 1 << 20, MaxQueryParams: 1 << 20})
	build := func(target int) string {
		var b strings.Builder
		b.WriteString("http://example.com")
		for b.Len() < target {
			b.WriteString("/a%2Fb/./%41")
		}
		return b.String()
	}
	u1, u64 := build(1<<10), build(64<<10)
	base := n.ScanCount()
	if _, err := n.Normalize(u1); err != nil {
		return false
	}
	s1 := n.ScanCount() - base
	base = n.ScanCount()
	if _, err := n.Normalize(u64); err != nil {
		return false
	}
	s64 := n.ScanCount() - base
	ratio := float64(s64) / float64(s1)
	return ratio > 32 && ratio < 128
}
