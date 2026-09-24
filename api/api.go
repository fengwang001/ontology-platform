// Package api is the public facade over hagg with a built-in self-check.
package api

import (
	"math/rand"
	"reflect"
	"sort"
	"strconv"

	"ontology/hagg"
	"ontology/hop"
)

type Result = hagg.Result

var ( // the four failure modes are re-exported distinct sentinel errors
	ErrBadParams   = hagg.ErrBadParams
	ErrClockBack   = hagg.ErrClockBack
	ErrTooManyOpen = hagg.ErrTooManyOpen
	ErrEmptyKey    = hagg.ErrEmptyKey
)

// Aggregator is the hopping-window counter; every call is atomic.
type Aggregator struct{ a *hagg.Agg }

// New requires size>0, slide>0 and size%slide==0.
func New(size, slide int64, maxOpen int) (*Aggregator, error) {
	a, err := hagg.New(size, slide, maxOpen)
	if err != nil {
		return nil, err
	}
	return &Aggregator{a}, nil
}
func (g *Aggregator) Add(key string, ts int64) error    { return g.a.Add(key, ts) }
func (g *Aggregator) Advance(t int64) ([]Result, error) { return g.a.Advance(t) }
func (g *Aggregator) Flush() []Result                   { return g.a.Flush() }
func (g *Aggregator) Results() []Result                 { return g.a.Results() }
func (g *Aggregator) Dropped() int64                    { return g.a.Dropped() }

const negInf int64 = -1 << 63

type accEvent struct {
	key       string
	ts, clock int64
}

// naiveRef brute-forces the spec (one entry per (key,window), (end,key) sorted) and the drop count.
func naiveRef(size, slide int64, evs []accEvent) ([]Result, int64) {
	p := hop.Params{Size: size, Slide: slide}
	cnt := map[string]map[int64]int64{}
	var drop int64
	for _, e := range evs {
		alive := false
		for _, k := range p.Ks(e.ts) {
			if w := p.At(k); w.End > e.clock {
				if cnt[e.key] == nil {
					cnt[e.key] = map[int64]int64{}
				}
				cnt[e.key][k]++
				alive = true
			}
		}
		if !alive {
			drop++
		}
	}
	var rs []Result
	for key, ms := range cnt {
		for k, n := range ms {
			w := p.At(k)
			rs = append(rs, Result{Key: key, K: k, Start: w.Start, End: w.End, Count: n})
		}
	}
	sort.Slice(rs, func(i, j int) bool {
		return rs[i].End < rs[j].End || rs[i].End == rs[j].End && rs[i].Key < rs[j].Key
	})
	return rs, drop
}

// SelfCheck replays a built-in randomized history against the brute-force
// reference and exercises every rejection path; scratch instances only.
func (g *Aggregator) SelfCheck() bool {
	return selfRandom() && selfMembership() && selfReject() && hagg.ScanBoundOK()
}

func selfRandom() bool {
	const steps = 2000
	rnd := rand.New(rand.NewSource(20260924))
	a, _ := hagg.New(12, 4, 1_000_000)
	evs := make([]accEvent, 0, steps)
	clock := negInf // no separate "set" flag: every legal t exceeds -inf
	for i := 0; i < steps; i++ {
		if rnd.Intn(3) == 0 {
			t := int64(rnd.Intn(90)) - 30
			if t < clock {
				t = clock
			}
			a.Advance(t) // params valid and t clamped: error impossible
			clock = t
			continue
		}
		key := "k" + strconv.Itoa(rnd.Intn(8))
		ts := int64(rnd.Intn(120)) - 40
		a.Add(key, ts) // non-empty key, huge maxOpen: error impossible
		evs = append(evs, accEvent{key, ts, clock})
	}
	a.Flush()
	want, drop := naiveRef(12, 4, evs)
	return reflect.DeepEqual(a.Results(), want) && a.Dropped() == drop
}

// selfMembership checks n-window admission at -inf and partial late admission.
func selfMembership() bool {
	a, _ := hagg.New(12, 4, 1000)
	atInf := a.Add("x", -5) == nil && len(a.Flush()) == 3
	b, _ := hagg.New(12, 4, 1000)
	_, e := b.Advance(0)
	late := e == nil && b.Add("x", -3) == nil // k=-3 closed at 0; -2,-1 admit it
	rs := b.Flush()
	return atInf && late && len(rs) == 2 && rs[0].K == -2 && rs[1].K == -1 && b.Dropped() == 0
}

// selfReject verifies the four distinct errors leave no trace and the instance stays usable.
func selfReject() bool {
	if _, e := New(0, 4, 10); e != ErrBadParams {
		return false
	}
	if ErrBadParams == ErrClockBack || ErrClockBack == ErrTooManyOpen ||
		ErrTooManyOpen == ErrEmptyKey || ErrBadParams == ErrEmptyKey {
		return false
	}
	g, _ := New(12, 4, 3)
	tooMany := g.Add("a", 0) == nil && g.Add("b", 0) == ErrTooManyOpen && g.Add("", 0) == ErrEmptyKey
	noTrace := g.Dropped() == 0 && len(g.Results()) == 0 // rejected Adds changed nothing
	_, eb1 := g.Advance(5)                               // legal: closes a's k=-2
	_, eb2 := g.Advance(4)                               // rejected: clock stays 5
	_, eb3 := g.Advance(5)                               // equal time is legal; still usable
	rs := g.Flush()                                      // rejected b never counted: a's k=-1, k=0 remain
	return tooMany && noTrace &&
		eb1 == nil && eb2 == ErrClockBack && eb3 == nil &&
		len(rs) == 2 && rs[0].Key == "a" && rs[1].Key == "a"
}
