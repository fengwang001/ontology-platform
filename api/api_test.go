package api_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/hlc"
	"ontology/trace"
)

func TestTwelveStep(t *testing.T) {
	a, err := api.New(2, 5, 100)
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		node int
		pt   int64
		kind byte // 'l' local, 's' send, 'r' recv
		want hlc.T
	}{
		{0, 10, 's', hlc.T{L: 10, C: 0}}, {1, 11, 'r', hlc.T{L: 11, C: 0}},
		{1, 12, 's', hlc.T{L: 12, C: 0}}, {1, 11, 'l', hlc.T{L: 12, C: 1}},
		{1, 12, 'l', hlc.T{L: 12, C: 2}}, {1, 12, 'l', hlc.T{L: 12, C: 3}},
		{0, 7, 'r', hlc.T{L: 12, C: 1}}, {0, 12, 's', hlc.T{L: 12, C: 2}},
		{1, 12, 'r', hlc.T{L: 12, C: 4}}, {1, 12, 's', hlc.T{L: 12, C: 5}},
		{0, 15, 'l', hlc.T{L: 15, C: 0}}, {0, 14, 'r', hlc.T{L: 15, C: 1}},
	}
	var ids []int64
	for i, s := range steps {
		var got hlc.T
		var err error
		switch s.kind {
		case 'l':
			got, err = a.Local(s.node, s.pt)
		case 's':
			var id int64
			got, id, err = a.Send(s.node, 1-s.node, s.pt)
			ids = append(ids, id)
		case 'r':
			got, err = a.Recv(s.node, ids[0], s.pt)
			ids = ids[1:]
		}
		if err != nil || got != s.want {
			t.Fatalf("step %d: got %v,%v want %v", i+1, got, err, s.want)
		}
	}
}

// TestNaiveReferenceRandom checks invariants 1-3 black-box on random
// rollback readings interleaved with random send/recv.
func TestNaiveReferenceRandom(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		a, _ := api.New(4, 50, 1000)
		rng := rand.New(rand.NewSource(seed))
		var last [4]hlc.T
		var cmax, pt [4]int64
		sendTS := map[int64]hlc.T{}
		sendMax := map[int64]int64{}
		var mTo []int
		var mID []int64
		check := func(node int, ts hlc.T) {
			t.Helper()
			if ts.L != cmax[node] {
				t.Fatalf("seed %d node %d: l=%d, naive max pt=%d", seed, node, ts.L, cmax[node])
			}
			if last[node] != (hlc.T{}) && !(last[node].LE(ts) && last[node] != ts) {
				t.Fatalf("seed %d node %d: not strictly increasing", seed, node)
			}
			last[node] = ts
		}
		for range 300 {
			node := rng.Intn(4)
			pt[node] = max(0, pt[node]+rng.Int63n(11)-5) // rollback mixed in
			op := rng.Intn(3)
			if op == 2 && len(mID) == 0 {
				op = 0
			}
			var ts hlc.T
			var err error
			var id, rid int64 = -1, -1
			switch op {
			case 0:
				ts, err = a.Local(node, pt[node])
			case 1:
				to := rng.Intn(4)
				ts, id, err = a.Send(node, to, pt[node])
				mTo, mID = append(mTo, to), append(mID, id)
			default:
				j := rng.Intn(len(mID))
				node, rid = mTo[j], mID[j]
				mTo, mID = append(mTo[:j], mTo[j+1:]...), append(mID[:j], mID[j+1:]...)
				ts, err = a.Recv(node, rid, pt[node])
			}
			if errors.Is(err, hlc.ErrOffset) { // rejected: message stays
				mTo, mID = append(mTo, node), append(mID, rid)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			cmax[node] = max(cmax[node], pt[node])
			switch {
			case rid >= 0:
				cmax[node] = max(cmax[node], sendMax[rid])
				if !sendTS[rid].LE(ts) || sendTS[rid] == ts {
					t.Fatalf("seed %d: recv not strictly after send", seed)
				}
			case id >= 0:
				sendTS[id], sendMax[id] = ts, cmax[node]
			}
			check(node, ts)
		}
	}
}

func TestFailureAtomicity(t *testing.T) {
	for _, cfg := range []struct{ n, off, mc int64 }{{0, 5, 1}, {1, -1, 1}, {1, 5, 0}} {
		if _, err := api.New(int(cfg.n), cfg.off, cfg.mc); !errors.Is(err, trace.ErrParam) {
			t.Fatalf("New%v: %v", cfg, err)
		}
	}
	a, _ := api.New(2, 5, 100)
	_, _ = a.Local(0, 3)
	_, id, _ := a.Send(0, 1, 10)
	total := func() int {
		n0, _ := a.CountUpTo(0, 1<<62, 1<<62)
		n1, _ := a.CountUpTo(1, 1<<62, 1<<62)
		return n0 + n1
	}
	before := total()
	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"bad node", func() error { _, e := a.Local(9, 1); return e }, trace.ErrParam},
		{"negative pt", func() error { _, e := a.Local(0, -1); return e }, trace.ErrParam},
		{"unknown msg", func() error { _, e := a.Recv(1, 999, 4); return e }, trace.ErrNoMsg},
		{"wrong node", func() error { _, e := a.Recv(0, id, 10); return e }, trace.ErrNotYours},
		{"offset", func() error { _, e := a.Recv(1, id, 4); return e }, hlc.ErrOffset},
	}
	for _, c := range cases {
		if err := c.fn(); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		if total() != before {
			t.Fatalf("%s mutated state", c.name)
		}
	}
	small, _ := api.New(1, 5, 1)
	_, _ = small.Local(0, 1)
	_, _ = small.Local(0, 1)
	if _, err := small.Local(0, 1); !errors.Is(err, hlc.ErrCounter) {
		t.Fatalf("counter: got %v", err)
	}
	seen := map[error]bool{}
	for _, e := range []error{trace.ErrParam, trace.ErrNoMsg, trace.ErrNotYours, trace.ErrReceived, hlc.ErrOffset, hlc.ErrCounter} {
		if seen[e] {
			t.Fatalf("duplicate sentinel %v", e)
		}
		seen[e] = true
	}
	if _, err := a.Recv(1, id, 5); err != nil { // 10-5==maxOffset: accepted
		t.Fatal(err)
	}
	if _, err := a.Recv(1, id, 6); !errors.Is(err, trace.ErrReceived) {
		t.Fatalf("double receive: got %v", err)
	}
}

func TestSelfCheck(t *testing.T) {
	if a, err := api.New(2, 5, 100); err != nil {
		t.Fatal(err)
	} else if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
