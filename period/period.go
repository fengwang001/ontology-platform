// Package period derives period properties of a string from its prefix function.
package period

import "ontology/pfx"

// MinPeriod returns the smallest p in [1, len(s)] such that s[i] == s[i-p]
// for all i in [p, len(s)). By the period/border duality this is
// len(s) - (longest border). Returns 0 for the empty string.
func MinPeriod(s string) int {
	n := len(s)
	if n == 0 {
		return 0
	}
	return n - pfx.Build(s).LongestBorder()
}

// IsPeriodic reports whether p is a period of s by direct definition:
// 1 <= p <= len(s) and s[i] == s[i-p] for all i in [p, len(s)).
// Out-of-range p yields false.
func IsPeriodic(s string, p int) bool {
	n := len(s)
	if p < 1 || p > n {
		return false
	}
	for i := p; i < n; i++ {
		if s[i] != s[i-p] {
			return false
		}
	}
	return true
}

// PrefixPeriods returns the minimum period of every prefix s[0..i].
func PrefixPeriods(s string) []int {
	n := len(s)
	out := make([]int, n)
	if n == 0 {
		return out
	}
	pf := pfx.Build(s)
	for i := 0; i < n; i++ {
		out[i] = i + 1 - pf.At(i)
	}
	return out
}

// IsPower reports whether s is t^k for some proper prefix t and k >= 2,
// i.e. there is a period p < len(s) with len(s) % p == 0.
func IsPower(s string) bool {
	n := len(s)
	if n == 0 {
		return false
	}
	p := MinPeriod(s)
	return p < n && n%p == 0
}
