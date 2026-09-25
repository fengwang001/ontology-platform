// Package tpart maintains a (key, bucket) count view with rolling
// retention: only the most recent r buckets below/at cur are kept.
package tpart

import (
	"sync"

	"ontology/tbucket"
)

// Part is a time-partitioned materialized count view.
type Part struct {
	size int64
	r    int64

	mu      sync.RWMutex
	counts  map[int64]map[string]int64 // bucket -> key -> count
	cur     int64                      // max bucket key seen so far
	hasCur  bool                       // false until the first event
	lo      int64                      // retention floor cur-r+1 (valid when hasCur)
	dropped int64                      // late drops + cleaned-bucket drops

	checked int64 // buckets inspected during the last advance+cleanup
}

// New returns an empty view. size and r must be positive (caller checks).
func New(size, r int64) *Part {
	return &Part{size: size, r: r, counts: map[int64]map[string]int64{}}
}

// Add records one event. A late event (bucket below the retention
// floor) is dropped and counted; cleaned buckets are never reopened.
func (p *Part) Add(ts int64, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := tbucket.Key(ts, p.size)
	if p.hasCur && k < p.lo {
		p.dropped++
		return
	}
	if !p.hasCur || k > p.cur {
		p.advance(k)
	}
	b := p.counts[k]
	if b == nil {
		b = map[string]int64{}
		p.counts[k] = b
	}
	b[key]++
}

// advance moves cur up to k and cleans every bucket below the new
// retention floor, walking only the newly expired key range.
func (p *Part) advance(k int64) {
	p.checked = 0
	if !p.hasCur {
		p.cur, p.lo, p.hasCur = k, k-p.r+1, true
		return
	}
	newLo := k - p.r + 1
	for b := p.lo; b < newLo; b++ {
		p.checked++
		for _, c := range p.counts[b] {
			p.dropped += c
		}
		delete(p.counts, b)
	}
	p.cur, p.lo = k, newLo
}

// Snapshot returns the view as key -> bucket -> count.
func (p *Part) Snapshot() map[string]map[int64]int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := map[string]map[int64]int64{}
	for b, keys := range p.counts {
		for key, c := range keys {
			m := out[key]
			if m == nil {
				m = map[int64]int64{}
				out[key] = m
			}
			m[b] = c
		}
	}
	return out
}

// Dropped returns the total number of dropped events.
func (p *Part) Dropped() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dropped
}
