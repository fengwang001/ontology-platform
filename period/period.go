// Package period derives string periods from the pfx border array.
package period

import "ontology/pfx"

// MinPeriod returns the smallest period p of s (1 ≤ p ≤ len(s)):
// p = len(s) - π[len(s)-1]. It returns 0 only for the empty string.
func MinPeriod(s string) int {
	if len(s) == 0 {
		return 0
	}
	t := pfx.Build(s)
	return t.Len() - t.PiAt(t.Len()-1)
}

// IsPeriodic reports whether p is a period of s by the definition:
// for every i in [p, len(s)), s[i] == s[i-p]. p == len(s) always qualifies;
// p ≤ 0 or p > len(s) never does.
func IsPeriodic(s string, p int) bool {
	n := len(s)
	if p <= 0 || p > n {
		return false
	}
	for i := p; i < n; i++ {
		if s[i] != s[i-p] {
			return false
		}
	}
	return true
}

// PrefixPeriods returns, for each prefix s[0..i], its smallest period,
// i.e. result[i] = (i+1) - π[i].
func PrefixPeriods(s string) []int {
	t := pfx.Build(s)
	out := make([]int, t.Len())
	for i := range out {
		out[i] = (i + 1) - t.PiAt(i)
	}
	return out
}

// IsPower reports whether s is a whole-number repetition s = t^k, k ≥ 2:
// the smallest period p satisfies p < len(s) and len(s) % p == 0.
// A string can have a period < len(s) without being a power ("abababa").
func IsPower(s string) bool {
	n := len(s)
	if n < 2 {
		return false
	}
	p := MinPeriod(s)
	return p < n && n%p == 0
}
