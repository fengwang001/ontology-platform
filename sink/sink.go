// Package sink holds the persistent region (table, watermarks, duplicate
// count) and the in-memory working state, commits batches atomically and
// rebuilds working state from the persistent region on Restart.
package sink

import (
	"errors"
	"sync"

	"ontology/wm"
)

// ErrTooManyPartitions: existing plus new partitions exceed maxPartitions.
var ErrTooManyPartitions = errors.New("sink: partition count exceeds maxPartitions")

// region is the persistent state: result table, per-partition high
// watermarks and the duplicate counter.
type region struct {
	table map[string]int64
	wm    map[int]int64
	dups  int64
}

func (r *region) clone() region {
	t := make(map[string]int64, len(r.table))
	for k, v := range r.table {
		t[k] = v
	}
	w := make(map[int]int64, len(r.wm))
	for p, o := range r.wm {
		w[p] = o
	}
	return region{table: t, wm: w, dups: r.dups}
}

// Sink is a concurrency-safe idempotent sink.
type Sink struct {
	mu   sync.Mutex
	max  int
	pers region // persistent region, survives Restart
	work region // in-memory working state

	checked int // entries inspected while rebuilding work on last Restart
}

// New creates an empty sink allowing up to maxPartitions partitions.
func New(maxPartitions int) *Sink {
	s := &Sink{max: maxPartitions}
	s.pers = region{table: map[string]int64{}, wm: map[int]int64{}}
	s.work = s.pers.clone()
	return s
}

// Write validates and atomically commits one batch: the batch is applied
// to a clone and swapped in only when every check has passed, so any
// rejection leaves table, watermarks and duplicate count untouched.
func (s *Sink) Write(batch []wm.Rec) error {
	if err := wm.Validate(batch); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh := map[int]bool{}
	for _, r := range batch {
		if _, ok := s.work.wm[r.Partition]; !ok {
			fresh[r.Partition] = true
		}
	}
	if len(s.work.wm)+len(fresh) > s.max {
		return ErrTooManyPartitions
	}
	next := s.work.clone()
	for _, r := range batch {
		w := int64(-1)
		if cur, ok := next.wm[r.Partition]; ok {
			w = cur
		}
		if wm.Apply(w, r.Offset) {
			next.table[r.Key] += r.Val
			next.wm[r.Partition] = r.Offset
		} else {
			next.dups++
		}
	}
	s.pers = next
	s.work = next.clone()
	return nil
}

// Restart simulates a crash: working state is rebuilt by reading the
// persistent region directly — one entry per watermark plus one per table
// key — never by replaying applied records.
func (s *Sink) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = len(s.pers.wm) + len(s.pers.table)
	s.work = s.pers.clone()
}

// Table returns a copy of the current result table.
func (s *Sink) Table() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.work.clone().table
}

// Watermark returns the high watermark of partition p, -1 if never applied.
func (s *Sink) Watermark(p int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.work.wm[p]; ok {
		return w
	}
	return -1
}

// Duplicates returns the total number of dropped duplicate records.
func (s *Sink) Duplicates() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.work.dups
}
