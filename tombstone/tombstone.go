package tombstone

import (
	"bytes"
	"errors"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
)

var (
	ErrInvalidRange = errors.New("tombstone: start key must be less than end key")
	ErrZeroSeq      = errors.New("tombstone: sequence number must be positive")
	ErrInvalidCap   = errors.New("tombstone: capacity is not positive")
	ErrCapExceeded  = errors.New("tombstone: tombstone count reached capacity")
)

type Tombstone struct {
	Start []byte
	End   []byte
	Seq   uint64
}

type Fragment struct {
	Start []byte
	End   []byte
	Seqs  []uint64
}

type Fragmenter struct {
	mu         sync.RWMutex
	capacity   int
	tombstones []Tombstone
	fragments  []Fragment
	dirty      bool
	examined   atomic.Int64
}

func New(capacity int) *Fragmenter {
	return &Fragmenter{capacity: capacity}
}

func (f *Fragmenter) Register(s, e []byte, q uint64) error {
	if bytes.Compare(s, e) >= 0 {
		return ErrInvalidRange
	}
	if q == 0 {
		return ErrZeroSeq
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.capacity <= 0 {
		return ErrInvalidCap
	}
	if len(f.tombstones) >= f.capacity {
		return ErrCapExceeded
	}
	f.tombstones = append(f.tombstones, Tombstone{
		Start: bytes.Clone(s),
		End:   bytes.Clone(e),
		Seq:   q,
	})
	f.dirty = true
	return nil
}

func (f *Fragmenter) Covered(k []byte, q, snap uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rebuild()
	frags := f.fragments
	lo, hi := 0, len(frags)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		f.examined.Add(1)
		if bytes.Compare(frags[mid].Start, k) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	idx := lo - 1
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

func (f *Fragmenter) Fragments() []Fragment {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rebuild()
	out := make([]Fragment, len(f.fragments))
	for i, frag := range f.fragments {
		out[i] = Fragment{
			Start: bytes.Clone(frag.Start),
			End:   bytes.Clone(frag.End),
			Seqs:  slices.Clone(frag.Seqs),
		}
	}
	return out
}

func (f *Fragmenter) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.tombstones)
}

func (f *Fragmenter) rebuild() {
	if !f.dirty {
		return
	}
	f.dirty = false
	type event struct {
		key   []byte
		seq   uint64
		start bool
	}
	events := make([]event, 0, 2*len(f.tombstones))
	for _, ts := range f.tombstones {
		events = append(events, event{ts.Start, ts.Seq, true}, event{ts.End, ts.Seq, false})
	}
	sort.Slice(events, func(i, j int) bool {
		return bytes.Compare(events[i].key, events[j].key) < 0
	})
	active := make(map[uint64]int)
	var frags []Fragment
	for i := 0; i < len(events); {
		j := i
		for j < len(events) && bytes.Equal(events[j].key, events[i].key) {
			j++
		}
		for k := i; k < j; k++ {
			if !events[k].start {
				active[events[k].seq]--
				if active[events[k].seq] == 0 {
					delete(active, events[k].seq)
				}
			}
		}
		for k := i; k < j; k++ {
			if events[k].start {
				active[events[k].seq]++
			}
		}
		if j == len(events) || len(active) == 0 {
			i = j
			continue
		}
		seqs := make([]uint64, 0, len(active))
		for seq := range active {
			seqs = append(seqs, seq)
		}
		slices.Sort(seqs)
		slices.Reverse(seqs)
		segStart, segEnd := events[i].key, events[j].key
		if n := len(frags); n > 0 &&
			bytes.Equal(frags[n-1].End, segStart) &&
			slices.Equal(frags[n-1].Seqs, seqs) {
			frags[n-1].End = bytes.Clone(segEnd)
			i = j
			continue
		}
		frags = append(frags, Fragment{
			Start: bytes.Clone(segStart),
			End:   bytes.Clone(segEnd),
			Seqs:  seqs,
		})
		i = j
	}
	f.fragments = frags
}
