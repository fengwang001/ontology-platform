package api

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func genTokens(r *rand.Rand, n, steps int) []string {
	max, toks := n, make([]string, 0, steps)
	for i := 0; i < steps; i++ {
		switch k := r.Intn(10); {
		case k <= 1:
			toks, max = append(toks, "r"), max+1
		case k <= 4:
			toks = append(toks, "d"+strconv.Itoa(r.Intn(max+2)))
		default:
			toks = append(toks, "a"+strconv.Itoa(r.Intn(max+2)))
		}
	}
	return toks
}

// TestNaiveReferenceEquivalence pins invariant 1: 50 random sequences stay field-identical to the naive ref.
func TestNaiveReferenceEquivalence(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 1 + r.Intn(4)
		p, _ := NewPhaser(n)
		x, _ := newNaive(n)
		for i, tok := range genTokens(r, n, 150) {
			if err := cmpStep(p, x, tok); err != nil {
				t.Fatalf("seed=%d step=%d: %v", seed, i, err)
			}
		}
	}
}

func TestSentinelsDistinct(t *testing.T) {
	all := []error{ErrBadN, ErrUnknownParty, ErrDuplicateArrival, ErrTerminated}
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[i] == all[j] {
				t.Fatalf("sentinels %d and %d are equal", i, j)
			}
		}
	}
}

// TestScenarios pins section 3 (甲)(乙)(丙) through the public API.
func TestScenarios(t *testing.T) {
	// 甲: C joins the CURRENT phase; no advance after B; C arrives at phase 0.
	p, _ := NewPhaser(2)
	r0, _ := p.Arrive(0)
	id, ph, _ := p.Register()
	r1, _ := p.Arrive(1)
	if r0 != 0 || id != 2 || ph != 0 || r1 != 0 || p.Phase() != 0 {
		t.Fatal("甲: early advance or wrong register phase")
	}
	r2, _ := p.Arrive(id)
	if r2 != 0 || p.Phase() != 1 {
		t.Fatalf("甲: C arrives %d then phase %d, want 0/1", r2, p.Phase())
	}
	// 乙: B arrives, then A arrive-and-deregister => exactly phase 1.
	q, _ := NewPhaser(2)
	q.Arrive(1)
	v, err := q.ArriveAndDeregister(0)
	if err != nil || v != 0 || q.Phase() != 1 {
		t.Fatalf("乙: ret=%d phase=%d err=%v", v, q.Phase(), err)
	}
	w, _ := NewPhaser(2) // 丙: last arriver returns pre-advance phase
	a, _ := w.Arrive(0)
	b, _ := w.Arrive(1)
	if a != 0 || b != 0 || w.Phase() != 1 {
		t.Fatalf("丙: returns %d,%d phase %d, want 0,0/1", a, b, w.Phase())
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, e := NewPhaser(0); !errors.Is(e, ErrBadN) {
		t.Fatalf("bad n err=%v", e)
	}
	cases := []struct {
		name   string
		setup  func(*Phaser)
		reject func(*Phaser) error
		want   error
	}{
		{"unknown", nil, func(p *Phaser) error { _, e := p.Arrive(9); return e }, ErrUnknownParty},
		{"duplicate", func(p *Phaser) { p.Arrive(0) },
			func(p *Phaser) error { _, e := p.Arrive(0); return e }, ErrDuplicateArrival},
		{"terminated", func(p *Phaser) { p.ArriveAndDeregister(0); p.ArriveAndDeregister(1) },
			func(p *Phaser) error { _, _, e := p.Register(); return e }, ErrTerminated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _ := NewPhaser(2)
			if c.setup != nil {
				c.setup(p)
			}
			before := p.Snapshot()
			if err := c.reject(p); !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
			if !reflect.DeepEqual(p.Snapshot(), before) {
				t.Fatalf("state changed: %+v vs %+v", p.Snapshot(), before)
			}
		})
	}
}

func TestConcurrentBarrier(t *testing.T) {
	const N, K = 8, 50
	p, _ := NewPhaser(N)
	var perRound [K + 1]int64
	done := make(chan struct{}, N)
	for g := 0; g < N; g++ {
		go func(id int) {
			for round := 0; round < K; round++ {
				got, err := p.Arrive(id)
				if err != nil || got != round || p.AwaitAdvance(round) != round+1 {
					t.Errorf("party %d round %d: got=%d err=%v", id, round, got, err)
					return
				}
				atomic.AddInt64(&perRound[round+1], 1)
			}
			done <- struct{}{}
		}(g)
	}
	for g := 0; g < N; g++ {
		<-done
	}
	for k := 1; k <= K; k++ {
		if atomic.LoadInt64(&perRound[k]) != N {
			t.Fatalf("round %d had %d parties, want %d", k, perRound[k], N)
		}
	}
	if p.Phase() != K {
		t.Fatalf("final phase=%d, want %d", p.Phase(), K)
	}
}
