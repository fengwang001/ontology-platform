// Package api is the public facade over the CDC applier; it depends only on applier.
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/applier"
)

type (
	Event    = applier.Event
	Kind     = applier.Kind
	Decision = applier.Decision
)

const (
	Applied      = applier.Applied
	DeadLettered = applier.DeadLettered
)

var (
	ErrInvalid   = applier.ErrInvalid
	ErrSeqOrder  = applier.ErrSeqOrder
	ErrBufferCap = applier.ErrBufferCap
	ErrSelfCheck = errors.New("self-check failed")
)

type Applier struct{ a *applier.Applier }

func New(maxAttempts, maxBuffered int, fails map[Event]int) (*Applier, error) {
	a, err := applier.New(maxAttempts, maxBuffered, fails)
	if err != nil {
		return nil, err
	}
	return &Applier{a: a}, nil
}
func (p *Applier) Submit(e Event) ([]Decision, error) { return p.a.Submit(e) }
func (p *Applier) Tick() []Decision                   { return p.a.Tick() }
func (p *Applier) DeadLetters() []Event               { return p.a.DeadLetters() }
func (p *Applier) Applied() []Event                   { return p.a.Applied() }

// prefixOK verifies invariant 1: per key, the finalized seq set is {1..h},
// i.e. an exact prefix with no gaps.
func prefixOK(ap *Applier, keys []string) bool {
	fin := map[string]map[int64]bool{}
	for _, evs := range [][]Event{ap.Applied(), ap.DeadLetters()} {
		for _, ev := range evs {
			if fin[ev.Key] == nil {
				fin[ev.Key] = map[int64]bool{}
			}
			fin[ev.Key][ev.Seq] = true
		}
	}
	for _, k := range keys {
		for s := int64(1); s <= int64(len(fin[k])); s++ {
			if !fin[k][s] {
				return false
			}
		}
	}
	return true
}

func seqString(evs []Event, k string) string {
	out := ""
	for _, ev := range evs {
		if ev.Key == k {
			out += fmt.Sprintf("%d,", ev.Seq)
		}
	}
	return out
}

// SelfCheck verifies the four invariants through the public surface only.
func (p *Applier) SelfCheck() error {
	const maxA, maxB = 3, 400
	iso, _ := New(maxA, 8, map[Event]int{{Key: "A", Seq: 1}: 1})
	iso.Submit(Event{Key: "A", Seq: 1}) // A blocked at attempt 1
	ds, err := iso.Submit(Event{Key: "C", Seq: 1})
	if err != nil || len(ds) != 1 || ds[0].Kind != Applied { // invariant 2
		return fmt.Errorf("%w: invariant 2 fault isolation", ErrSelfCheck)
	}
	bad := []struct {
		m, b int
		f    map[Event]int
	}{{0, 1, nil}, {1, -1, nil}, {1, 1, map[Event]int{{Key: "Z", Seq: 1}: -1}}}
	for _, c := range bad { // invariant 4a: invalid arguments
		if _, e := New(c.m, c.b, c.f); !errors.Is(e, ErrInvalid) {
			return fmt.Errorf("%w: invalid-argument class", ErrSelfCheck)
		}
	}
	before := len(iso.Applied()) + len(iso.DeadLetters())
	tot := func(p *Applier) int { return len(p.Applied()) + len(p.DeadLetters()) }
	if _, e := iso.Submit(Event{Key: "", Seq: 1}); !errors.Is(e, ErrInvalid) || tot(iso) != before {
		return fmt.Errorf("%w: empty-key class / no-trace", ErrSelfCheck)
	}
	if _, e := iso.Submit(Event{Key: "A", Seq: 1}); !errors.Is(e, ErrSeqOrder) || tot(iso) != before {
		return fmt.Errorf("%w: seq-order class / no-trace", ErrSelfCheck)
	}
	full, _ := New(1, 0, map[Event]int{{Key: "Z", Seq: 1}: 1})
	full.Submit(Event{Key: "Z", Seq: 1})
	if _, e := full.Submit(Event{Key: "Z", Seq: 2}); !errors.Is(e, ErrBufferCap) || tot(full) != 0 {
		return fmt.Errorf("%w: buffer-cap class / no-trace", ErrSelfCheck)
	}
	rnd := rand.New(rand.NewSource(20260924)) // invariants 1 & 3
	for trial := 0; trial < 12; trial++ {
		keys := []string{"k1", "k2", "k3", "k4", "k5"}
		rf, order, cnt := map[Event]int{}, []Event{}, map[string]int64{}
		for i := 0; i < 300; i++ {
			k := keys[rnd.Intn(len(keys))]
			cnt[k]++
			ev := Event{Key: k, Seq: cnt[k]}
			order = append(order, ev)
			if rnd.Intn(3) == 0 {
				rf[ev] = rnd.Intn(maxA + 2)
			}
		}
		ap, _ := New(maxA, maxB, rf)
		for i, ev := range order {
			if _, e := ap.Submit(ev); e != nil {
				return e
			}
			if i%7 == 0 {
				ap.Tick()
			}
			if !prefixOK(ap, keys) {
				return fmt.Errorf("%w: invariant 1 prefix gap", ErrSelfCheck)
			}
		}
		for len(ap.Applied())+len(ap.DeadLetters()) < len(order) {
			ap.Tick()
		}
		for _, k := range keys {
			expA, expD := "", ""
			for s := int64(1); s <= cnt[k]; s++ {
				if rf[Event{Key: k, Seq: s}] >= maxA {
					expD += fmt.Sprintf("%d,", s)
				} else {
					expA += fmt.Sprintf("%d,", s)
				}
			}
			if seqString(ap.Applied(), k) != expA || seqString(ap.DeadLetters(), k) != expD {
				return fmt.Errorf("%w: invariant 3 key %s", ErrSelfCheck, k)
			}
		}
	}
	return nil
}
