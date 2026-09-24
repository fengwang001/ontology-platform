// Package api is the public entry point to the foreign-key materialization.
// An RWMutex serializes access so views and SelfCheck are concurrency-safe.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/fk"
	"ontology/sch"
)

// DB is an in-memory, concurrency-safe pair of tables P and C.
type DB struct {
	mu sync.RWMutex
	st *sch.State
	e  *fk.Engine
}

// New returns an empty DB.
func New() *DB {
	st := sch.NewState()
	return &DB{st: st, e: fk.New(st)}
}

func (d *DB) PIns(pk string) error     { d.mu.Lock(); defer d.mu.Unlock(); return d.e.PIns(pk) }
func (d *DB) PDel(pk string) error     { d.mu.Lock(); defer d.mu.Unlock(); return d.e.PDel(pk) }
func (d *DB) CIns(ck, pk string) error { d.mu.Lock(); defer d.mu.Unlock(); return d.e.CIns(ck, pk) }
func (d *DB) CDel(ck string) error     { d.mu.Lock(); defer d.mu.Unlock(); return d.e.CDel(ck) }

// ViewP returns a sorted snapshot copy of the parent keys.
func (d *DB) ViewP() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.st.Parents()
}

// ViewC returns a snapshot copy: ck -> parent (sort keys for presentation).
func (d *DB) ViewC() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.st.Children()
}

// scStep op kinds + payload: kCIns uses a=ck,b=parent; otherwise a is key; want nil=success.
const kPIns, kPDel, kCIns, kCDel = 0, 1, 2, 3

type scStep struct {
	op   int
	a, b string
	want error
}

func thirteen() []scStep {
	E, R, N := fk.ErrOrphanChild, fk.ErrParentReferenced, fk.ErrChildNotFound
	return []scStep{
		{kCIns, "k1", "p1", E}, {kPIns, "p1", "", nil}, {kCIns, "k1", "p1", nil},
		{kCIns, "k2", "p2", E}, {kPDel, "p1", "", R}, {kCIns, "k3", "p1", nil},
		{kCDel, "k1", "", nil}, {kCDel, "k2", "", N}, {kCDel, "k3", "", nil},
		{kPDel, "p1", "", nil}, {kCIns, "k3", "p1", E}, {kPIns, "p1", "", nil},
		{kCIns, "k3", "p1", nil},
	}
}

func (d *DB) run(s scStep) error {
	switch s.op {
	case kPIns:
		return d.PIns(s.a)
	case kPDel:
		return d.PDel(s.a)
	case kCIns:
		return d.CIns(s.a, s.b)
	default:
		return d.CDel(s.a)
	}
}

// naiveApply mirrors an accepted op on the independent plain-map replay.
func naiveApply(np map[string]struct{}, nc map[string]string, s scStep) {
	switch s.op {
	case kPIns:
		np[s.a] = struct{}{}
	case kPDel:
		delete(np, s.a)
	case kCIns:
		if _, ok := nc[s.a]; !ok {
			nc[s.a] = s.b
		}
	default:
		delete(nc, s.a)
	}
}

// checkInvariants verifies inv. 2 (no dangling) and 3 (ref counts).
func (d *DB) checkInvariants() error {
	alive, group := map[string]bool{}, map[string]int{}
	for _, pk := range d.ViewP() {
		alive[pk] = true
	}
	for ck, p := range d.ViewC() {
		if !alive[p] {
			return fmt.Errorf("invariant 2 violated: child %q dangles to %q", ck, p)
		}
		group[p]++
	}
	for p := range alive {
		if got := d.st.RefCountOf(p); got != group[p] {
			return fmt.Errorf("invariant 3 violated: parent %q count=%d want %d", p, got, group[p])
		}
	}
	return nil
}

// SelfCheck replays the built-in thirteen-step sequence on a fresh DB and
// verifies all four invariants: expected verdicts + naive replay of accepted
// ops (1), no dangling row after each accepted step (2), reference counts (3),
// byte-identical state across every rejection (4). The receiver is untouched.
func (d *DB) SelfCheck() error {
	f := New()
	np, nc := map[string]struct{}{}, map[string]string{}
	for i, s := range thirteen() {
		bp, bc := f.ViewP(), f.ViewC()
		err := f.run(s)
		if !errors.Is(err, s.want) {
			return fmt.Errorf("step %d: got %v want %v", i+1, err, s.want)
		}
		if err != nil {
			if ap, ac := f.ViewP(), f.ViewC(); !reflect.DeepEqual(bp, ap) || !reflect.DeepEqual(bc, ac) {
				return fmt.Errorf("step %d: rejected op mutated state", i+1)
			}
			continue
		}
		if err := f.checkInvariants(); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		naiveApply(np, nc, s)
	}
	wp := make([]string, 0, len(np))
	for p := range np {
		wp = append(wp, p)
	}
	sort.Strings(wp)
	if !reflect.DeepEqual(f.ViewP(), wp) || !reflect.DeepEqual(f.ViewC(), nc) {
		return errors.New("invariant 1 violated: views differ from naive replay")
	}
	return nil
}
