// Package match implements the streaming KMP state machine: it holds
// the pattern, its prefix function and the current state, and turns
// fed chunks into absolute end indices of (overlapping) matches.
package match

import (
	"errors"

	"ontology/pfx"
)

// ErrTooManyMatches is returned by Feed when a chunk would push the
// accumulated match count past the configured limit. The whole chunk
// is rejected and no state changes.
var ErrTooManyMatches = errors.New("kmp: match limit exceeded")

// Matcher is a streaming KMP matcher for one pattern. It is not
// safe for concurrent use; callers must serialize access.
type Matcher struct {
	pat  []byte
	pi   []int
	j    int // current state: suffix/prefix match length, always < len(pat)
	pos  int // absolute index of the next byte to consume
	hits []int
	// cmps counts character comparisons in the matching phase. It is
	// deliberately unexported and unreachable from any public API.
	cmps       int
	maxMatches int
	dropped    int // matches discarded; always 0 under reject-whole-batch
}

// New builds a Matcher for pattern p (must be non-empty) allowing at
// most maxMatches accumulated matches.
func New(p []byte, maxMatches int) *Matcher {
	pat := make([]byte, len(p))
	copy(pat, p)
	return &Matcher{pat: pat, pi: pfx.Compute(pat), maxMatches: maxMatches}
}

// Feed consumes one chunk and returns the absolute end indices of the
// matches first completed within it. An empty chunk is a no-op. If the
// chunk would push the total match count past the limit, the whole
// chunk is rejected with ErrTooManyMatches and no state changes: the
// batch is simulated on local copies and committed only on success.
func (m *Matcher) Feed(chunk []byte) ([]int, error) {
	if len(chunk) == 0 {
		return nil, nil
	}
	j, pos, cmps := m.j, m.pos, m.cmps
	var hits []int
	for _, c := range chunk {
		j = pfx.Step(m.pat, m.pi, j, c, &cmps)
		if j == len(m.pat) {
			hits = append(hits, pos)
			j = m.pi[len(m.pi)-1]
		}
		pos++
	}
	if len(m.hits)+len(hits) > m.maxMatches {
		return nil, ErrTooManyMatches
	}
	m.j, m.pos, m.cmps = j, pos, cmps
	m.hits = append(m.hits, hits...)
	return hits, nil
}

// Hits returns a copy of the end indices collected so far.
func (m *Matcher) Hits() []int {
	out := make([]int, len(m.hits))
	copy(out, m.hits)
	return out
}
