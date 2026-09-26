package api_test

import "errors"
import "math/rand"
import "sync"
import "testing"
import "ontology/api"
import "ontology/quorum"

func TestTenStep(t *testing.T) {
	p := api.New(3)
	ops := [10][3]int{{0, 2, 0}, {1, 2, 0}, {2, 2, 0}, {0, 2, 10}, {1, 2, 10}, {0, 3, 0}, {1, 3, 0}, {2, 3, 0}, {0, 3, 10}, {1, 3, 10}}
	wantV := [...]int{0, 0, 0, 0, 10, 10, 10, 10, 10, 10}
	for i, o := range ops {
		if (231>>uint(i))&1 == 1 { // S1-S3, S6-S8 are prepares
			if _, _, _, e := p.Prepare(o[0], o[1]); e != nil {
				t.Fatalf("S%d prepare: %v", i+1, e)
			}
		} else if ok, e := p.Accept(o[0], o[1], o[2]); !ok || e != nil {
			t.Fatalf("S%d accept: %v,%v", i+1, ok, e)
		}
		v, ok := p.Chosen()
		if ok != (i >= 4) || v != wantV[i] {
			t.Fatalf("S%d Chosen = (%d,%v), want (%d,%v)", i+1, v, ok, wantV[i], i >= 4)
		}
	}
	if ok, an, av, e := p.Prepare(2, 5); !ok || e != nil || an != 0 || av != 0 {
		t.Fatal("acc2 never accepted; higher prepare must report (0,0)")
	}
}
func TestSelfCheck(t *testing.T) {
	if !api.New(3).SelfCheck() {
		t.Fatal("SelfCheck must verify all four invariants on its built-in sequences")
	}
}

// TestRejectionLeavesState: three distinct sentinels + no-trace refusals (invariant 4).
func TestRejectionLeavesState(t *testing.T) {
	cases := []struct {
		name string
		call func(p *api.Paxos) error
		want error
	}{
		{"prepare index low", func(p *api.Paxos) error { _, _, _, e := p.Prepare(-1, 5); return e }, api.ErrAcceptorIndex},
		{"accept index out of range", func(p *api.Paxos) error { _, e := p.Accept(3, 5, 1); return e }, api.ErrAcceptorIndex},
		{"prepare non-positive", func(p *api.Paxos) error { _, _, _, e := p.Prepare(0, 0); return e }, api.ErrProposalNumber},
		{"accept non-positive", func(p *api.Paxos) error { _, e := p.Accept(0, -2, 1); return e }, api.ErrProposalNumber},
		{"prepare stale", func(p *api.Paxos) error { _, _, _, e := p.Prepare(0, 2); return e }, api.ErrStalePrepare},
	}
	for _, tc := range cases {
		p := api.New(3)
		_, _, _, _ = p.Prepare(0, 2)
		_, _ = p.Accept(0, 2, 10)
		if e := tc.call(p); !errors.Is(e, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, e, tc.want)
		}
		if refused, e := p.Accept(0, 1, 99); refused || e != nil {
			t.Errorf("%s: stale accept must be (false,nil)", tc.name)
		}
		if _, _, _, e := p.Prepare(0, 10); e != nil { // fails if promised was corrupted upward
			t.Fatalf("%s: probe prepare: %v", tc.name, e)
		}
		if ok, an, av, e := p.Prepare(0, 11); !ok || e != nil || an != 2 || av != 10 {
			t.Fatalf("%s: accepted state changed: %v,%d,%d,%v", tc.name, ok, an, av, e)
		}
		if ok, _ := p.Accept(1, 11, 55); !ok {
			t.Fatalf("%s: instance not usable after rejection", tc.name)
		}
	}
}

// TestSafety: randomized multi-round Paxos vs shadow oracle; at most one chosen.
func TestSafety(t *testing.T) {
	for _, m := range []int{1, 3, 5, 9} {
		rng := rand.New(rand.NewSource(int64(m)*13 + 1))
		p, held := api.New(m), map[int]int{}
		chosenOnce, theChosen := false, 0
		for round, n := 0, 1; round < 30; round, n = round+1, n+1 {
			order, rep := rng.Perm(m), []quorum.Report{}
			for _, acc := range order[:m/2+1] {
				ok, an, av, e := p.Prepare(acc, n)
				if !ok || e != nil {
					t.Fatalf("m=%d prepare: %v %v", m, ok, e)
				}
				if an > 0 {
					rep = append(rep, quorum.Report{Accepted: an, Value: av, HasValue: true})
				}
			}
			v := quorum.PickValue(rep, 1000+round)
			for _, acc := range order[:m/2+1] {
				if ok, _ := p.Accept(acc, n, v); !ok {
					t.Fatalf("m=%d accept refused", m)
				}
				held[acc] = v
			}
			counts := map[int]int{}
			for _, x := range held {
				counts[x]++
			}
			ov, ook := 0, false
			for x, c := range counts {
				if c >= m/2+1 {
					ov, ook = x, true
				}
			}
			gv, gok := p.Chosen()
			if gv != ov || gok != ook {
				t.Fatalf("m=%d round=%d: (%d,%v) != oracle (%d,%v)", m, round, gv, gok, ov, ook)
			}
			if gok {
				if chosenOnce && gv != theChosen {
					t.Fatalf("m=%d: two distinct values chosen: %d then %d", m, theChosen, gv)
				}
				chosenOnce, theChosen = true, gv
			}
		}
	}
}

// TestConcurrentChosen: concurrent readers see field-identical Chosen (go test -race).
func TestConcurrentChosen(t *testing.T) {
	p := api.New(5)
	for _, a := range []int{0, 2, 4} {
		_, _, _, _ = p.Prepare(a, 1)
		if ok, _ := p.Accept(a, 1, 42); !ok {
			t.Fatal("seed accept")
		}
	}
	const g = 24
	var wg sync.WaitGroup
	start := make(chan struct{})
	vs, hs := make([]int, g), make([]bool, g)
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for k := 0; k < 2000; k++ {
				vs[i], hs[i] = p.Chosen()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < g; i++ {
		if vs[i] != 42 || !hs[i] {
			t.Fatalf("reader %d saw (%d,%v), want (42,true)", i, vs[i], hs[i])
		}
	}
}
