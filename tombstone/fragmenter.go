// Package tombstone implements a range-tombstone fragmenter.
//
// Tombstones (s, e, q) cover the half-open key range [s, e) with a
// positive sequence number q. The fragmenter splits all registered
// tombstones into non-overlapping half-open fragments, each recording
// the deduplicated, descendingly sorted set of covering sequence
// numbers. Adjacent fragments with identical sequence sets are merged,
// so the fragment table is uniquely determined by the tombstone set.
package tombstone

import (
	"bytes"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

var (
	// ErrInvalidRange is returned when the start key is not less than
	// the end key.
	ErrInvalidRange = errors.New("tombstone: start key must be less than end key")
	// ErrZeroSeq is returned when the sequence number is zero.
	ErrZeroSeq = errors.New("tombstone: sequence number must be positive")
	// ErrNotConstructing is returned when Cap is not positive, i.e. the
	// fragmenter is not in a constructing state.
	ErrNotConstructing = errors.New("tombstone: fragmenter is not constructing (Cap <= 0)")
	// ErrCapExceeded is returned when the tombstone count already
	// reached Cap.
	ErrCapExceeded = errors.New("tombstone: tombstone count reached Cap")
)

// Fragment is a non-overlapping half-open key range [Start, End)
// together with the deduplicated, descendingly sorted sequence numbers
// of all tombstones covering it.
type Fragment struct {
	Start []byte
	End   []byte
	Seqs  []uint64
}

type tombstoneRange struct {
	start []byte
	end   []byte
	seq   uint64
}

// Fragmenter registers range tombstones and maintains the unique
// fragmented view of them. It is safe for concurrent use.
type Fragmenter struct {
	// Cap is the maximum number of tombstones that may be registered.
	// A non-positive Cap means the fragmenter is not constructing and
	// rejects every registration.
	Cap int

	mu         sync.RWMutex
	tombstones []tombstoneRange
	fragments  []Fragment
	dirty      bool

	// examined counts how many fragments Covered inspected across all
	// calls since the last ResetExamined.
	examined atomic.Int64
}

// New returns a Fragmenter with the given capacity.
func New(capacity int) *Fragmenter {
	return &Fragmenter{Cap: capacity}
}

// Register adds the tombstone [s, e) with sequence number q.
//
// Validation order: invalid range, zero sequence, non-constructing
// state (Cap <= 0), capacity reached. Only the first failing reason is
// reported and a rejected call leaves the fragment table and the
// tombstone count untouched.
func (f *Fragmenter) Register(s, e []byte, q uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if bytes.Compare(s, e) >= 0 {
		return ErrInvalidRange
	}
	if q == 0 {
		return ErrZeroSeq
	}
	if f.Cap <= 0 {
		return ErrNotConstructing
	}
	if len(f.tombstones) >= f.Cap {
		return ErrCapExceeded
	}

	sc := append([]byte(nil), s...)
	ec := append([]byte(nil), e...)
	f.tombstones = append(f.tombstones, tombstoneRange{start: sc, end: ec, seq: q})
	f.dirty = true
	return nil
}

// Count returns the number of registered tombstones.
func (f *Fragmenter) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.tombstones)
}

// Fragments returns a copy of the current fragment table.
func (f *Fragmenter) Fragments() []Fragment {
	f.ensureFragments()
	f.mu.RLock()
	defer f.mu.RUnlock()
	return cloneFragments(f.fragments)
}

// Examined returns the number of fragments inspected by Covered calls
// since the last ResetExamined.
func (f *Fragmenter) Examined() int64 {
	return f.examined.Load()
}

// ResetExamined resets the fragment-examination counter.
func (f *Fragmenter) ResetExamined() {
	f.examined.Store(0)
}

// Covered reports whether the point record (k, q) is covered at
// snapshot snap: true iff some tombstone covering k has a sequence
// number t with q < t <= snap. No validation is done on q or snap;
// when snap < q the formula yields false.
func (f *Fragmenter) Covered(k []byte, q, snap uint64) bool {
	f.ensureFragments()
	f.mu.RLock()
	defer f.mu.RUnlock()

	frags := f.fragments
	// Binary search for the last fragment with Start <= k.
	idx := sort.Search(len(frags), func(i int) bool {
		f.examined.Add(1)
		return bytes.Compare(frags[i].Start, k) > 0
	}) - 1
	if idx < 0 {
		return false
	}
	f.examined.Add(1)
	frag := frags[idx]
	if bytes.Compare(k, frag.End) >= 0 {
		return false
	}
	for _, t := range frag.Seqs {
		if q < t && t <= snap {
			return true
		}
	}
	return false
}

// ensureFragments rebuilds the fragment table if registration marked
// it stale. Rebuilds are lazy so that bulk registration stays cheap.
func (f *Fragmenter) ensureFragments() {
	f.mu.RLock()
	dirty := f.dirty
	f.mu.RUnlock()
	if !dirty {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dirty {
		f.rebuildLocked()
		f.dirty = false
	}
}

// rebuildLocked recomputes the fragment table from all registered
// tombstones with a sweep over the sorted endpoints. Callers must
// hold f.mu (write).
func (f *Fragmenter) rebuildLocked() {
	points := make([][]byte, 0, 2*len(f.tombstones))
	for _, ts := range f.tombstones {
		points = append(points, ts.start, ts.end)
	}
	sort.Slice(points, func(i, j int) bool {
		return bytes.Compare(points[i], points[j]) < 0
	})
	uniq := points[:0]
	for _, p := range points {
		if len(uniq) == 0 || !bytes.Equal(uniq[len(uniq)-1], p) {
			uniq = append(uniq, p)
		}
	}

	starts := append([]tombstoneRange(nil), f.tombstones...)
	sort.Slice(starts, func(i, j int) bool {
		return bytes.Compare(starts[i].start, starts[j].start) < 0
	})
	ends := append([]tombstoneRange(nil), f.tombstones...)
	sort.Slice(ends, func(i, j int) bool {
		return bytes.Compare(ends[i].end, ends[j].end) < 0
	})

	active := make(map[uint64]int)
	si, ei := 0, 0
	var frags []Fragment
	for i, lo := range uniq {
		for ei < len(ends) && bytes.Compare(ends[ei].end, lo) <= 0 {
			seq := ends[ei].seq
			active[seq]--
			if active[seq] == 0 {
				delete(active, seq)
			}
			ei++
		}
		for si < len(starts) && bytes.Compare(starts[si].start, lo) <= 0 {
			active[starts[si].seq]++
			si++
		}
		if i+1 >= len(uniq) || len(active) == 0 {
			continue
		}
		hi := uniq[i+1]
		seqs := make([]uint64, 0, len(active))
		for seq := range active {
			seqs = append(seqs, seq)
		}
		sort.Slice(seqs, func(a, b int) bool { return seqs[a] > seqs[b] })
		if n := len(frags); n > 0 && equalSeqs(frags[n-1].Seqs, seqs) &&
			bytes.Equal(frags[n-1].End, lo) {
			frags[n-1].End = append([]byte(nil), hi...)
			continue
		}
		frags = append(frags, Fragment{
			Start: append([]byte(nil), lo...),
			End:   append([]byte(nil), hi...),
			Seqs:  seqs,
		})
	}
	f.fragments = frags
}

func equalSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneFragments(frags []Fragment) []Fragment {
	out := make([]Fragment, len(frags))
	for i, fr := range frags {
		out[i] = Fragment{
			Start: append([]byte(nil), fr.Start...),
			End:   append([]byte(nil), fr.End...),
			Seqs:  append([]uint64(nil), fr.Seqs...),
		}
	}
	return out
}
