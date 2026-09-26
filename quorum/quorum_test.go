package quorum

import (
	"math/rand"
	"testing"

	"ontology/acpt"
)

// TestBookkeeping pins one acceptor's judgments (invariant 2 at unit level):
// Prepare is strict (n > promised), Accept uses >= (n == promised succeeds),
// stale calls refuse without trace, promised is monotone. The spec's literal
// "accepted>=promised" is impossible at S1 (promised=2, accepted=0); what
// holds is promised monotone and, on protocol rounds, promised >= accepted.
func TestBookkeeping(t *testing.T) {
	a := acpt.New()
	if ok, _, _ := a.Promise(2); !ok {
		t.Fatal("first promise must be granted")
	}
	if ok, _, _ := a.Promise(2); ok {
		t.Fatal("equal proposal must be refused (strict >)")
	}
	if ok, _, _ := a.Promise(1); ok {
		t.Fatal("lower proposal must be refused")
	}
	if a.Accept(1, 99) || a.Accepted() != 0 || a.AcceptedValue() != 0 {
		t.Fatal("stale accept must refuse before any accept, no trace")
	}
	if !a.Accept(2, 10) || a.Accepted() != 2 || a.AcceptedValue() != 10 {
		t.Fatal("n == promised must be accepted (>= rule, question 丙)")
	}
	if a.Accept(1, 99) || a.Accepted() != 2 || a.AcceptedValue() != 10 {
		t.Fatal("stale accept must refuse and leave no trace (question 甲)")
	}
	if ok, an, av := a.Promise(3); !ok || an != 2 || av != 10 {
		t.Fatalf("fresh promise must report accepted state, got %v,%d,%d", ok, an, av)
	}
	if a.Promised() != 3 || a.Promised() < a.Accepted() {
		t.Fatal("promised must be monotone and >= accepted")
	}
}

func TestPickValue(t *testing.T) {
	r := func(n, v int, has bool) Report { return Report{Accepted: n, Value: v, HasValue: has} }
	cases := []struct {
		name    string
		reports []Report
		own     int
		want    int
	}{
		{"no reports adopts own", nil, 20, 20},
		{"section-three round 3 adopts 10", []Report{r(2, 10, true), r(2, 10, true), r(0, 0, false)}, 20, 10},
		{"largest accepted number wins", []Report{r(2, 10, true), r(5, 77, true)}, 20, 77},
		{"only never-accepted reports adopts own", []Report{r(0, 0, false), r(0, 0, false)}, 9, 9},
		{"tied accepted numbers share one value", []Report{r(3, 4, true), r(3, 4, true)}, 1, 4},
	}
	for _, tc := range cases {
		if got := PickValue(tc.reports, tc.own); got != tc.want {
			t.Errorf("%s: PickValue = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestNaiveAgreement drives acceptors in randomized arrival order and checks
// invariant 1 after every step: incremental tally == full-table Naive.
func TestNaiveAgreement(t *testing.T) {
	for _, m := range []int{1, 3, 5, 11} {
		rng := rand.New(rand.NewSource(int64(m) + 7))
		as := make([]*acpt.Acceptor, m)
		for i := range as {
			as[i] = acpt.New()
		}
		tl := New(m)
		for step := 0; step < 300; step++ {
			acc := rng.Perm(m)[0]
			n := 1 + rng.Intn(8)
			as[acc].Promise(n)
			if rng.Intn(2) == 0 {
				old := as[acc].AcceptedValue()
				if as[acc].Accept(n, 1+rng.Intn(5)) {
					tl.Move(old, as[acc].AcceptedValue())
				}
			}
			v1, ok1 := tl.Chosen()
			v2, ok2 := Naive(as, m/2+1)
			if v1 != v2 || ok1 != ok2 {
				t.Fatalf("m=%d step=%d: tally=(%d,%v) naive=(%d,%v)", m, step, v1, ok1, v2, ok2)
			}
		}
	}
}

// TestChosenReadsBounded proves Chosen is not a table scan: for odd m from
// 101 to 9999, acceptors accept one value in random order and the number of
// acceptor entries read by Chosen must stay under the m-independent bound.
func TestChosenReadsBounded(t *testing.T) {
	sizes := []int{101, 1001, 5001, 9999}
	reads := make([]int, len(sizes))
	for k, m := range sizes {
		rng := rand.New(rand.NewSource(42))
		as := make([]*acpt.Acceptor, m)
		for i := range as {
			as[i] = acpt.New()
			as[i].Promise(1)
		}
		tl := New(m)
		for _, j := range rng.Perm(m) { // all acceptors accept 7, random order
			old := as[j].AcceptedValue()
			if !as[j].Accept(1, 7) {
				t.Fatalf("m=%d: accept refused", m)
			}
			tl.Move(old, 7)
		}
		if v, ok := tl.Chosen(); !ok || v != 7 {
			t.Fatalf("m=%d: Chosen = (%d,%v), want (7,true)", m, v, ok)
		}
		reads[k] = int(tl.lastReads.Load()) // white-box: the counter is unexported
	}
	for k, m := range sizes {
		if reads[k] > readBound {
			t.Fatalf("m=%d: Chosen read %d acceptors, bound is %d (must not grow with m)", m, reads[k], readBound)
		}
	}
	if reads[0] != reads[len(reads)-1] {
		t.Fatalf("reads grew with m: %v", reads)
	}
}
