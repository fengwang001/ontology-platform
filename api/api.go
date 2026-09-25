// Package api is the public face of the phaser. It depends only on bar.
package api

import (
	"errors"
	"maps"
	"reflect"
	"strconv"
	"sync"

	"ontology/bar"
)

// Decidable sentinel errors (pairwise distinct).
var (
	ErrBadN             = bar.ErrBadN
	ErrUnknownParty     = bar.ErrUnknownParty
	ErrDuplicateArrival = bar.ErrDuplicateArrival
	ErrTerminated       = bar.ErrTerminated
)

// Phaser is a reusable phase synchronization barrier with dynamic parties.
type Phaser struct{ b *bar.Bar }

// NewPhaser starts at phase 0 with n registered parties 0..n-1.
func NewPhaser(n int) (*Phaser, error) {
	bb, err := bar.New(n)
	if err != nil {
		return nil, err
	}
	return &Phaser{bb}, nil
}

func (p *Phaser) Register() (int, int, error)             { return p.b.Register() }
func (p *Phaser) Arrive(id int) (int, error)              { return p.b.Arrive(id) }
func (p *Phaser) ArriveAndDeregister(id int) (int, error) { return p.b.ArriveAndDeregister(id) }
func (p *Phaser) AwaitAdvance(was int) int                { return p.b.AwaitAdvance(was) }
func (p *Phaser) Phase() int                              { return p.b.Phase() }

// Snapshot is a point-in-time copy, for tests and SelfCheck.
type Snapshot struct {
	Phase     int
	Parties   map[int]struct{}
	Unarrived map[int]struct{}
}

func (p *Phaser) Snapshot() Snapshot { ph, pp, uu := p.b.Snapshot(); return Snapshot{ph, pp, uu} }

// naiveRef is the independent plain reference guarded by one sync.Mutex.
type naiveRef struct {
	mu                 sync.Mutex
	parties, unarrived map[int]struct{}
	next, phase        int
}

func newNaive(n int) (*naiveRef, error) {
	if n <= 0 {
		return nil, ErrBadN
	}
	x := &naiveRef{parties: map[int]struct{}{}, unarrived: map[int]struct{}{}, next: n}
	for i := 0; i < n; i++ {
		x.parties[i], x.unarrived[i] = struct{}{}, struct{}{}
	}
	return x, nil
}

// step performs 'r' register, 'a' arrive, 'd' arrive-and-deregister. It
// returns the new id (for r) or the arrived phase (for a/d) and the error.
func (x *naiveRef) step(kind byte, id int) (int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.phase < 0 {
		return 0, ErrTerminated
	}
	if kind == 'r' {
		id = x.next
		x.next++
		x.parties[id], x.unarrived[id] = struct{}{}, struct{}{}
		return id, nil
	}
	if _, ok := x.parties[id]; !ok {
		return 0, ErrUnknownParty
	}
	if _, ok := x.unarrived[id]; !ok {
		return 0, ErrDuplicateArrival
	}
	p := x.phase
	delete(x.unarrived, id)
	if len(x.unarrived) == 0 && len(x.parties) > 0 {
		x.phase++
		for k := range x.parties {
			x.unarrived[k] = struct{}{}
		}
	}
	if kind == 'd' {
		delete(x.parties, id)
		delete(x.unarrived, id)
		if len(x.parties) == 0 {
			x.phase, x.unarrived = -1, map[int]struct{}{}
		}
	}
	return p, nil
}

func (x *naiveRef) snap() Snapshot {
	x.mu.Lock()
	defer x.mu.Unlock()
	return Snapshot{x.phase, maps.Clone(x.parties), maps.Clone(x.unarrived)}
}

// cmpStep runs one token ('r','a<id>','d<id>') on phaser and naive ref and compares value, error, full state.
func cmpStep(p *Phaser, x *naiveRef, tok string) error {
	id, _ := strconv.Atoi(tok[1:])
	var v int
	var e error
	switch tok[0] {
	case 'r':
		v, _, e = p.Register()
	case 'a':
		v, e = p.Arrive(id)
	default:
		v, e = p.ArriveAndDeregister(id)
	}
	v2, e2 := x.step(tok[0], id)
	if v != v2 || e != e2 || !reflect.DeepEqual(p.Snapshot(), x.snap()) {
		return errors.New("api: divergence at " + tok)
	}
	return nil
}

// SelfCheck verifies the four invariants on built-in op sequences: section 3
// four steps, 乙, unknown/duplicate rejections, and termination.
func SelfCheck() error {
	scripts := [][]string{
		{"a0", "r", "a1", "a2"},  // section 3
		{"a1", "d0"},             // 乙: arrive before deregister
		{"a9", "a0", "a0", "a1"}, // unknown id + duplicate leave no trace
		{"d0", "d1", "r", "a0"},  // terminated rejects register/arrive
	}
	for _, sc := range scripts {
		p, _ := NewPhaser(2)
		x, _ := newNaive(2)
		for _, tok := range sc {
			if err := cmpStep(p, x, tok); err != nil {
				return err
			}
		}
	}
	return nil
}
