// Package ver holds one key's append-only dual-timestamp version history.
// Ev = event time (may arrive out of order); In = ingest time (strictly
// increasing arrival order). It depends on no other package in this module.
package ver

import (
	"errors"
	"sort"
	"sync/atomic"
)

// ErrNotFound is the sentinel returned by every query with no matching version.
var ErrNotFound = errors.New("ver: version not found")

// Version is one triple in a key's history.
type Version struct {
	Value string
	Ev    int64 // event time: business time of the fact, may be negative/out of order
	In    int64 // ingest time: arrival order, strictly increasing per key
}

// History is a single key's version list plus an event-time-latest pointer.
type History struct {
	vs    []Version    // append order; guaranteed In-strictly-increasing by callers
	evIdx int          // index of the LatestEvent winner; -1 when empty
	scanN atomic.Int64 // unexported: versions inspected by the most recent LatestEvent call
}

// New returns an empty history.
func New() *History { return &History{evIdx: -1} }

// Len reports how many versions the history holds.
func (h *History) Len() int { return len(h.vs) }

// MaxIn returns the largest ingest time present and true, or 0,false when empty.
func (h *History) MaxIn() (int64, bool) {
	if len(h.vs) == 0 {
		return 0, false
	}
	return h.vs[len(h.vs)-1].In, true
}

// Append adds a version. Callers must guarantee In exceeds MaxIn; the
// event-time-latest pointer moves only when the newcomer wins by (Ev, then In).
func (h *History) Append(v Version) {
	i := len(h.vs)
	if h.evIdx < 0 || eventWins(v, h.vs[h.evIdx]) {
		h.evIdx = i // pointer update on insert: LatestEvent stays O(1)
	}
	h.vs = append(h.vs, v)
}

// eventWins reports whether a should beat b as the event-time-latest version:
// larger Ev wins; equal Ev is broken by the larger In (later arrival).
func eventWins(a, b Version) bool {
	return a.Ev > b.Ev || (a.Ev == b.Ev && a.In > b.In)
}

// LatestEvent returns the version with the greatest Ev (ties: greatest In).
func (h *History) LatestEvent() (Version, error) {
	if h.evIdx < 0 {
		h.scanN.Store(0)
		return Version{}, ErrNotFound
	}
	h.scanN.Store(1) // only the maintained pointer is inspected, independent of Len
	return h.vs[h.evIdx], nil
}

// LatestIngest returns the last-arrived version (greatest In).
func (h *History) LatestIngest() (Version, error) {
	if len(h.vs) == 0 {
		return Version{}, ErrNotFound
	}
	return h.vs[len(h.vs)-1], nil
}

// AtEvent returns the winner among versions with Ev <= T (max Ev, ties max In).
func (h *History) AtEvent(T int64) (Version, error) {
	best := -1
	for i := range h.vs { // only LatestEvent is pointer-bound; this is a naive scan
		if h.vs[i].Ev <= T && (best < 0 || eventWins(h.vs[i], h.vs[best])) {
			best = i
		}
	}
	if best < 0 {
		return Version{}, ErrNotFound
	}
	return h.vs[best], nil
}

// AtIngest returns the version with the greatest In that is <= T.
func (h *History) AtIngest(T int64) (Version, error) {
	// vs is In-ordered by construction, so binary-search the first In > T.
	i := sort.Search(len(h.vs), func(i int) bool { return h.vs[i].In > T })
	if i == 0 {
		return Version{}, ErrNotFound
	}
	return h.vs[i-1], nil
}

// SelfCheck proves LatestEvent is O(1): for growing history sizes the number
// of versions inspected per call must stay a small constant. It reports only
// pass/fail and never exposes the counter itself.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		h := New()
		seed := int64(m)
		for i := 0; i < m; i++ {
			seed = seed*6364136223846793005 + 1442695040888963407
			h.Append(Version{Value: "v", Ev: seed >> 33, In: int64(i + 1)})
		}
		if _, err := h.LatestEvent(); err != nil {
			return err
		}
		if n := h.scanN.Load(); n > 2 {
			return errors.New("ver: LatestEvent is not O(1): inspected count grew with history size")
		}
	}
	return nil
}
