// Package compact folds a windowed change log down to the latest record
// per key. It depends only on package rec.
package compact

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/rec"
)

// ErrBadWindow is returned when lo >= hi. Windows are half-open [lo,hi).
var ErrBadWindow = errors.New("compact: invalid window: lo >= hi")

// C is an in-memory, goroutine-safe folder. Fed records are bucketed by
// key, so locating one key's survivor never scans any other key's
// history.
type C struct {
	mu        sync.Mutex
	retention int64
	recs      map[string][]rec.Rec
	// cmps records, per key, how many in-window records were compared to
	// locate that key's survivor during the most recent Compact. It is
	// unexported and no exported method ever returns its values.
	cmps map[string]int
}

// New creates a folder with the given tombstone retention period.
func New(retention int64) *C {
	return &C{retention: retention, recs: map[string][]rec.Rec{}}
}

// Add appends records. Validation happens in package api.
func (c *C) Add(rs []rec.Rec) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range rs {
		c.recs[r.Key] = append(c.recs[r.Key], r)
	}
}

// View returns a copy of every fed record, ordered by (key, TS).
func (c *C) View() []rec.Rec {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]rec.Rec, 0)
	for _, rs := range c.recs {
		out = append(out, rs...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].TS < out[j].TS
	})
	return out
}

// Naive is the reference semantics: filter to [lo,hi), group by key,
// keep the max-TS record per key, then drop tombstones with TS+R <= hi.
func Naive(rs []rec.Rec, lo, hi, retention int64) []rec.Rec {
	best := map[string]rec.Rec{}
	for _, r := range rs {
		if r.TS < lo || r.TS >= hi {
			continue
		}
		if b, ok := best[r.Key]; !ok || r.TS > b.TS {
			best[r.Key] = r
		}
	}
	keys := make([]string, 0, len(best))
	for k := range best {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]rec.Rec, 0, len(keys))
	for _, k := range keys {
		r := best[k]
		if rec.Drop(r, hi, retention) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Compact returns the folded result for half-open window [lo,hi): each
// in-window key contributes only its max-TS record, unless that survivor
// is a tombstone with TS+R <= hi, in which case the key is absent.
func (c *C) Compact(lo, hi int64) ([]rec.Rec, error) {
	if lo >= hi {
		return nil, ErrBadWindow
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.recs))
	for k := range c.recs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cmps := map[string]int{}
	out := make([]rec.Rec, 0, len(keys))
	for _, k := range keys {
		var best rec.Rec
		found, n := false, 0
		for _, r := range c.recs[k] {
			if r.TS < lo || r.TS >= hi {
				continue
			}
			n++ // every in-window candidate is compared while locating the survivor
			if !found || r.TS > best.TS {
				best, found = r, true
			}
		}
		if n > 0 {
			cmps[k] = n
		}
		if !found || rec.Drop(best, hi, c.retention) {
			continue
		}
		out = append(out, best)
	}
	c.cmps = cmps
	return out, nil
}

// SelfCheck verifies, without exposing any counter value, that locating
// one key's survivor costs a small m-independent number of comparisons
// even when m other keys were fed. It returns nil on success.
func (c *C) SelfCheck() error {
	const bound = 3
	for _, m := range []int{100, 1000, 10000} {
		v := New(c.retention)
		rs := make([]rec.Rec, 0, m+1)
		for i := 0; i < m; i++ {
			rs = append(rs, rec.Rec{Key: fmt.Sprintf("k%05d", i), TS: int64(i + 1)})
		}
		rs = append(rs, rec.Rec{Key: "k00000", Value: 7, TS: int64(m + 1)})
		v.Add(rs)
		if _, err := v.Compact(0, int64(m)+2); err != nil {
			return err
		}
		if v.cmps["k00000"] > bound {
			return errors.New("compact: selfcheck: per-key fold comparisons not bounded")
		}
	}
	return nil
}
