// Package match implements greedy wildcard matching with backtracking.
// It depends only on parse.
package match

import (
	"fmt"

	"ontology/parse"
)

// matcher holds one matching run. Instances are never shared: every Match
// call creates a fresh one, so the unexported steps counter needs no
// synchronization and never reaches any public API.
type matcher struct {
	pattern []rune
	// steps counts forward advances of the text and pattern pointers
	// during a single run. It is deliberately unexported.
	steps int
}

// run is the greedy + backtracking two-pointer core over runes. At a '*'
// it first consumes nothing and lets later literals match; on failure it
// gives the star one more rune and retries, which is exactly backtracking.
func (m *matcher) run(text []rune) bool {
	si, pi := 0, 0
	starPi, starSi := -1, 0 // last '*' position; text index it currently eats
	for si < len(text) {
		switch {
		case pi < len(m.pattern) && (m.pattern[pi] == '?' || m.pattern[pi] == text[si]):
			si++
			pi++
			m.steps += 2 // one advance for each pointer
		case pi < len(m.pattern) && m.pattern[pi] == '*':
			starPi, starSi = pi, si
			pi++ // greedy start: the star eats the empty string
			m.steps++
		case starPi != -1:
			starSi++ // give the star one more rune, retry from after it
			si, pi = starSi, starPi+1
			m.steps++ // si moved forward; the pi reset is not an advance
		default:
			return false
		}
	}
	// Trailing stars must swallow the (empty) remainder.
	for pi < len(m.pattern) && m.pattern[pi] == '*' {
		pi++
		m.steps++
	}
	return pi == len(m.pattern)
}

// Match reports whether the whole text s is matched by pattern p.
// Invalid pattern or text never matches.
func Match(s, p string) bool {
	if err := parse.Validate(p); err != nil {
		return false
	}
	if err := parse.ValidateText(s); err != nil {
		return false
	}
	m := &matcher{pattern: []rune(p)}
	return m.run([]rune(s))
}

// naiveDP is the reference over runes: dp[i][j] for text prefix i and
// pattern prefix j. '*' = dp[i-1][j] || dp[i][j-1]; '?' / literal take
// dp[i-1][j-1].
func naiveDP(s, p string) bool {
	t, w := []rune(s), []rune(p)
	dp := make([]bool, len(w)+1)
	dp[0] = true
	for j := 1; j <= len(w); j++ {
		dp[j] = dp[j-1] && w[j-1] == '*'
	}
	for i := 1; i <= len(t); i++ {
		prev := dp[0]
		dp[0] = false
		for j := 1; j <= len(w); j++ {
			top := dp[j]
			switch w[j-1] {
			case '*':
				dp[j] = top || dp[j-1]
			case '?':
				dp[j] = prev
			default:
				dp[j] = prev && t[i-1] == w[j-1]
			}
			prev = top
		}
	}
	return dp[len(w)]
}

// SelfCheck verifies the match-level invariants against built-in pairs,
// including the linear step bound. It reports only pass/fail; the counter
// value itself is never exposed.
func SelfCheck() error {
	pairs := []struct {
		s, p string
		want bool
	}{
		{"adceb", "*a*b", true}, {"aab", "*ab", true}, {"ab", "a?b", false},
		{"ab", "a*", true}, {"acdcb", "a*c?b", false}, {"", "*", true},
		{"", "**", true}, {"abc", "a??", true}, {"abc", "a?c", true},
	}
	for _, q := range pairs {
		if Match(q.s, q.p) != q.want || Match(q.s, q.p) != naiveDP(q.s, q.p) {
			return fmt.Errorf("match: self-check pair %q vs %q", q.s, q.p)
		}
	}
	const n = 6 // "*" + four 'a' + "b": a backtracking-heavy pattern
	for _, mLen := range []int{100, 1000, 5000, 10000} {
		m := &matcher{pattern: []rune("*aaaab")}
		m.run([]rune(repeat('a', mLen)))
		if m.steps > 16*(n+mLen) {
			return fmt.Errorf("match: step count is not linear in n+m at m=%d", mLen)
		}
	}
	return nil
}

func repeat(r rune, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = r
	}
	return string(b)
}
