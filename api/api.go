// Package api is the public entry point of the per-key ordered CDC applier.
package api

import (
	"errors"
	"math/rand"
	"ontology/applier"
	"ontology/kq"
	"reflect"
)

type Event = kq.Event
type Record = applier.Record
type Kind = applier.Kind
type CDC struct{ a *applier.Applier }

const Applied, DeadLettered = applier.Applied, applier.DeadLettered

// The three rejection classes are distinct sentinel errors:
var ErrInvalidArgument, ErrSeqNotIncreasing, ErrBufferFull = applier.ErrInvalidArgument, applier.ErrSeqNotIncreasing, applier.ErrBufferFull

func ev(k string, s int64) Event          { return Event{Key: k, Seq: s} }
func rc(k string, s int64, d Kind) Record { return Record{Key: k, Seq: s, Kind: d} }

// New builds an applier; fails[e]=n: first n attempts of e fail.
func New(maxAttempts, maxBuffered int, fails map[Event]int) (*CDC, error) {
	if maxAttempts < 1 || maxBuffered < 0 {
		return nil, ErrInvalidArgument
	}
	for e, n := range fails {
		if n < 0 || e.Key == "" {
			return nil, ErrInvalidArgument
		}
	}
	return &CDC{a: applier.New(maxAttempts, maxBuffered, fails)}, nil
}
func (c *CDC) Submit(e Event) ([]Record, error) { return c.a.Submit(e) }
func (c *CDC) Tick() []Record                   { return c.a.Tick() }
func (c *CDC) Applied() []Event                 { return c.a.Applied() }
func (c *CDC) DeadLetters() []Event             { return c.a.DeadLetters() }
func settle(c *CDC, ma, n int) { // ma*n+1 ticks always finalize n events
	for i := 0; i < ma*n+1; i++ {
		c.Tick()
	}
}
func traceOf(c *CDC) map[string][]Record {
	m := map[string]map[int64]Kind{}
	tag := func(es []Event, d Kind) {
		for _, e := range es {
			if m[e.Key] == nil {
				m[e.Key] = map[int64]Kind{}
			}
			m[e.Key][e.Seq] = d
		}
	}
	tag(c.Applied(), Applied)
	tag(c.DeadLetters(), DeadLettered)
	g := map[string][]Record{}
	for k, sm := range m { // post-settle every seq 1..len is finalized (invariant 1)
		for s := int64(1); s <= int64(len(sm)); s++ {
			g[k] = append(g[k], rc(k, s, sm[s]))
		}
	}
	return g
}

// SelfCheck verifies the four invariants on built-in sequences.
func (c *CDC) SelfCheck() error {
	c1, _ := New(3, 8, map[Event]int{ev("A", 2): 1, ev("B", 1): 99, ev("B", 3): 1}) // (1)
	sub := []Event{ev("A", 1), ev("A", 2), ev("B", 1), ev("A", 3), ev("C", 1), ev("B", 2)}
	for i, e := range sub {
		rs, err := c1.Submit(e)
		if err != nil {
			return err
		} else if i == 4 && (len(rs) != 1 || rs[0] != rc("C", 1, Applied)) {
			return errors.New("invariant 2: C#1 must apply within Submit while A/B blocked")
		}
	}
	c1.Tick()
	c1.Submit(ev("B", 3))
	c1.Submit(ev("A", 4))
	settle(c1, 3, 9)
	want := map[string][]Record{"A": {rc("A", 1, Applied), rc("A", 2, Applied), rc("A", 3, Applied), rc("A", 4, Applied)}, "B": {rc("B", 1, DeadLettered), rc("B", 2, Applied), rc("B", 3, Applied)}, "C": {rc("C", 1, Applied)}}
	if !reflect.DeepEqual(traceOf(c1), want) { // invariants 1, 3
		return errors.New("invariant 1/3: mandated scenario mismatch")
	}
	rng := rand.New(rand.NewSource(278)) // (2) random vs serial reference
	for it := 0; it < 200; it++ {
		fl, all, pk := map[Event]int{}, []Event{}, map[string][]Event{}
		nk := 1 + rng.Intn(6)
		left, next, total := make([]int, nk), make([]int, nk), 0
		for k := range left {
			left[k], next[k] = 1+rng.Intn(8), 1
			total += left[k]
		}
		for total > 0 { // interleave keys while preserving each key's seq order
			k := rng.Intn(nk)
			if left[k] == 0 {
				continue
			}
			e := ev(string(rune('k'+k)), int64(next[k]))
			next[k], left[k], total = next[k]+1, left[k]-1, total-1
			all, pk[e.Key] = append(all, e), append(pk[e.Key], e)
			if rng.Intn(3) == 0 {
				fl[e] = rng.Intn(4)
			}
		}
		ma := 1 + rng.Intn(3)
		c2, _ := New(ma, 64, fl)
		for _, e := range all {
			c2.Submit(e)
			if rng.Intn(2) == 0 {
				c2.Tick()
			}
		}
		settle(c2, ma, len(all))
		w := map[string][]Record{}
		for _, es := range pk {
			for _, e := range es {
				d := Applied
				if fl[e] >= ma {
					d = DeadLettered
				}
				w[e.Key] = append(w[e.Key], rc(e.Key, e.Seq, d))
			}
		}
		if !reflect.DeepEqual(traceOf(c2), w) {
			return errors.New("invariant 1/3: random serial-reference mismatch")
		}
	}
	c3, _ := New(2, 1, map[Event]int{ev("z", 1): 5}) // (3) rejections leave no trace
	c3.Submit(ev("z", 1))
	c3.Submit(ev("z", 2))
	for _, t := range [][2]any{{ev("", 1), ErrInvalidArgument}, {ev("z", 1), ErrSeqNotIncreasing}, {ev("z", 3), ErrBufferFull}} {
		if _, g := c3.Submit(t[0].(Event)); !errors.Is(g, t[1].(error)) {
			return errors.New("invariant 4: rejection error class")
		}
	}
	if len(c3.Applied()) != 0 || len(c3.DeadLetters()) != 0 { // z1 blocked, z2 buffered: nothing finalized
		return errors.New("invariant 4: rejection changed state")
	}
	if _, e := New(0, 1, nil); !errors.Is(e, ErrInvalidArgument) {
		return errors.New("invariant 4: maxAttempts<1")
	} else if _, e = New(1, -1, nil); !errors.Is(e, ErrInvalidArgument) {
		return errors.New("invariant 4: maxBuffered<0")
	} else if _, e = New(1, 1, map[Event]int{ev("q", 1): -1}); !errors.Is(e, ErrInvalidArgument) {
		return errors.New("invariant 4: negative fail count")
	}
	return nil
}
