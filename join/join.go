// Package join holds the two stream buffers, matches events and emits outputs.
package join

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"

	"ontology/nkey"
)

type Side uint8

const L, R Side = 0, 1

type Row struct {
	Key *string
	Val int64
}
type Output struct {
	Key  string
	LVal int64
	RVal int64
}

var ErrSelfCheck = errors.New("join: self-check failed")

// Engine is the stream-stream inner join: non-NULL keys hash to
// insertion-ordered positions (O(matches) probe); NULL rows are buffered
// but never indexed and never match.
type Engine struct {
	mu         sync.Mutex
	lRows      []Row
	rRows      []Row
	lIdx       map[string][]int
	rIdx       map[string][]int
	out        []Output
	lastProbed int // unexported: opposite rows examined by latest Feed
}

func New() *Engine {
	return &Engine{lIdx: map[string][]int{}, rIdx: map[string][]int{}}
}

// Feed probes the opposite buffer in insertion order, then buffers the event.
func (e *Engine) Feed(sd Side, key *string, val int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastProbed = 0
	opRows, opIdx, ownRows, ownIdx := e.rRows, e.rIdx, &e.lRows, e.lIdx
	if sd == R {
		opRows, opIdx, ownRows, ownIdx = e.lRows, e.lIdx, &e.rRows, e.rIdx
	}
	if key != nil {
		for _, i := range opIdx[*key] {
			e.lastProbed++
			lv, rv := opRows[i].Val, val
			if sd == L {
				lv, rv = val, opRows[i].Val
			}
			e.out = append(e.out, Output{*key, lv, rv})
		}
	}
	*ownRows = append(*ownRows, Row{key, val})
	if key != nil {
		ownIdx[*key] = append(ownIdx[*key], len(*ownRows)-1) // no dedup
	}
}

// Snapshot returns a copy of all outputs emitted so far.
func (e *Engine) Snapshot() []Output {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Output(nil), e.out...)
}

// SelfCheck replays the eight-step derivation, checks random interleavings
// against a naive nested loop, and verifies the hash probe bound (the
// unexported counter stays O(hits)) at m = 100/1000/10000.
func (e *Engine) SelfCheck() error {
	j := New()
	k, q := "k", ""
	sd8 := []Side{R, L, L, R, R, L, R, L}
	ks8 := []*string{&k, &k, nil, nil, &k, &q, &q, &k}
	vs8 := []int64{100, 1, 2, 200, 101, 3, 300, 4}
	want := []int{0, 1, 1, 1, 2, 2, 3, 5}
	for i := range want {
		j.Feed(sd8[i], ks8[i], vs8[i])
		if n := len(j.Snapshot()); n != want[i] {
			return fmt.Errorf("%w: step %d got %d want %d", ErrSelfCheck, i+1, n, want[i])
		}
	}
	pool := []*string{nil, ptr(""), ptr("a"), ptr("b")}
	for t := 0; t < 10; t++ {
		g, rng := New(), rand.New(rand.NewSource(int64(t*7+1)))
		var ll, rr []Row
		for i := 0; i < 30; i++ {
			s, key, v := Side(rng.Intn(2)), pool[rng.Intn(4)], rng.Int63n(1000)
			before := len(g.Snapshot())
			g.Feed(s, key, v)
			if !slices.Equal(g.Snapshot()[before:], naive(ll, rr, s, key, v)) {
				return fmt.Errorf("%w: seed %d step %d", ErrSelfCheck, t, i)
			}
			buf := &ll
			if s == R {
				buf = &rr
			}
			*buf = append(*buf, Row{key, v})
		}
	}
	for _, m := range []int{100, 1000, 10000} {
		g := New()
		for i := range m {
			s := fmt.Sprintf("k%05d", i)
			g.Feed(R, &s, int64(i))
		}
		hit := "k00042"
		g.Feed(L, &hit, 7)
		if g.lastProbed != 1 || len(g.Snapshot()) != 1 {
			return fmt.Errorf("%w: probe=%d m=%d", ErrSelfCheck, g.lastProbed, m)
		}
		g.Feed(L, nil, 1)
		if g.lastProbed != 0 || len(g.Snapshot()) != 1 {
			return fmt.Errorf("%w: null probe=%d", ErrSelfCheck, g.lastProbed)
		}
	}
	return nil
}

func naive(ll, rr []Row, sd Side, key *string, v int64) []Output {
	var out []Output
	if sd == L {
		for _, r := range rr {
			if nkey.Matches(key, r.Key) {
				out = append(out, Output{*key, v, r.Val})
			}
		}
	} else {
		for _, l := range ll {
			if nkey.Matches(key, l.Key) {
				out = append(out, Output{*l.Key, l.Val, v})
			}
		}
	}
	return out
}

func ptr(s string) *string { return &s }
