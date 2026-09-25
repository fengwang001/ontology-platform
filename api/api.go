package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/backfill"
)

var (
	ErrInvalidW           = backfill.ErrInvalidW
	ErrBackfillOutOfRange = backfill.ErrBackfillOutOfRange
	ErrBackfillCompleted  = backfill.ErrBackfillCompleted
	ErrEmptyKey           = backfill.ErrEmptyKey
	ErrSelfCheck          = errors.New("api: self-check found an invariant violation")
)

type Event struct {
	Seq int64
	Key string
}
type Processor struct {
	mu    sync.RWMutex
	inner *backfill.View
}

func New(W int64) *Processor { return &Processor{inner: backfill.New(W)} }
func toInner(evs []Event) []backfill.Event {
	o := make([]backfill.Event, len(evs))
	for i := range evs {
		o[i] = backfill.Event{Seq: evs[i].Seq, Key: evs[i].Key}
	}
	return o
}
func (p *Processor) Backfill(evs []Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inner.ApplyBackfill(toInner(evs))
}
func (p *Processor) Online(evs []Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inner.ApplyOnline(toInner(evs))
}
func (p *Processor) CompleteBackfill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inner.Complete()
}
func (p *Processor) View() map[string]int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.inner.Counts()
}
func (p *Processor) Seen() int { p.mu.RLock(); defer p.mu.RUnlock(); return p.inner.Seen() }
func (p *Processor) SelfCheck() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if err := p.inner.SelfCheck(); err != nil {
		return err
	}
	return SelfCheckBuiltin()
}

var builtin = []struct {
	via byte
	seq int64
	key byte
	n   [4]int64
}{
	{'B', 1, 'a', [4]int64{1, 0, 0, 1}},
	{'B', 3, 'b', [4]int64{1, 1, 0, 2}},
	{'O', 12, 'b', [4]int64{1, 2, 0, 3}},
	{'B', 5, 'a', [4]int64{2, 2, 0, 4}},
	{'O', 5, 'a', [4]int64{2, 2, 0, 4}},
	{'B', 7, 'c', [4]int64{2, 2, 1, 5}},
	{'O', 11, 'c', [4]int64{2, 2, 2, 6}},
	{'B', 9, 'a', [4]int64{3, 2, 2, 7}},
	{'O', 3, 'b', [4]int64{3, 2, 2, 7}},
}

// SelfCheckBuiltin replays the built-in sequence on scratch instances.
func SelfCheckBuiltin() error {
	p := New(10)
	put := func(via byte, seq int64, key byte) error {
		e := []Event{{Seq: seq, Key: string(key)}}
		if via == 'B' {
			return p.Backfill(e)
		}
		return p.Online(e)
	}
	for _, s := range builtin {
		if err := put(s.via, s.seq, s.key); err != nil {
			return err
		}
		v := p.View()
		if [4]int64{v["a"], v["b"], v["c"], int64(p.Seen())} != s.n {
			return ErrSelfCheck
		}
	}
	final, seen := p.View(), p.Seen()
	ref, past := map[string]int64{}, map[int64]bool{}
	for _, s := range builtin { // invariant 1: equals naive deduped reference
		if !past[s.seq] {
			past[s.seq], ref[string(s.key)] = true, ref[string(s.key)]+1
		}
	}
	if !reflect.DeepEqual(final, ref) {
		return ErrSelfCheck
	}
	for _, s := range builtin { // invariant 3: re-delivery is a no-op
		if err := put(s.via, s.seq, s.key); err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(p.View(), final) || p.Seen() != seen {
		return ErrSelfCheck
	}
	q := New(10) // invariant 2: another interleaving + cutover agree
	for _, s := range builtin {
		if err := q.Online([]Event{{Seq: s.seq, Key: string(s.key)}}); err != nil {
			return err
		}
	}
	pre := q.View()
	if err := q.CompleteBackfill(); err != nil || !reflect.DeepEqual(pre, q.View()) ||
		!reflect.DeepEqual(q.View(), final) || q.Seen() != seen {
		return ErrSelfCheck
	}
	r := New(10) // invariant 4: rejected batches leave no trace
	if err := r.Backfill([]Event{{Seq: 1, Key: "a"}}); err != nil {
		return err
	}
	rv, rs := r.View(), r.Seen()
	if !errors.Is(r.Backfill([]Event{{Seq: 1, Key: "a"}, {Seq: 10, Key: "d"}}), ErrBackfillOutOfRange) ||
		!errors.Is(r.Online([]Event{{Seq: 1, Key: ""}}), ErrEmptyKey) {
		return ErrSelfCheck
	}
	if err := r.CompleteBackfill(); err != nil {
		return err
	}
	if !errors.Is(r.Backfill([]Event{{Seq: 2, Key: "a"}}), ErrBackfillCompleted) ||
		!errors.Is(New(-1).Online([]Event{{Seq: 1, Key: "a"}}), ErrInvalidW) ||
		!reflect.DeepEqual(r.View(), rv) || r.Seen() != rs {
		return ErrSelfCheck
	}
	return nil
}
