package router

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/part"
)

func seed3(t *testing.T) *Router {
	t.Helper()
	r := New()
	for _, id := range []int{0, 1, 2} {
		if err := r.AddPartition(id); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestEightStepTable(t *testing.T) {
	r := seed3(t)
	ops := []func() error{
		func() error { return r.Assign("a", 0) }, func() error { return r.Assign("b", 0) },
		func() error { return r.Assign("c", 1) }, func() error { return r.Assign("d", 2) },
		func() error { return r.BeginDrain(2) }, func() error { return r.Assign("e", 2) },
		func() error { return r.Put("d", "x") }, func() error { return r.Migrate(2, 0) },
	}
	wantErr := []bool{false, false, false, false, false, true, true, false}
	wantCnt := [][3]int{{1, 0, 0}, {2, 0, 0}, {2, 1, 0}, {2, 1, 1}, {2, 1, 1}, {2, 1, 1}, {2, 1, 1}, {3, 1, 0}}
	for i, op := range ops {
		err := op()
		got := [3]int{r.Count(0), r.Count(1), r.Count(2)}
		dv, dhave := r.Get("d") // Draining stays readable; value always absent
		bad := (err != nil) != wantErr[i] || got != wantCnt[i] || (i >= 3 && (dhave || dv != ""))
		if bad {
			t.Fatalf("step %d err=%v counts=%v want %v Get(d)=(%q,%v)", i+1, err, got, wantCnt[i], dv, dhave)
		}
	}
	if r.parts[2].State() != part.Removed || r.Count(2) != 0 {
		t.Fatal("P2 must be Removed and empty")
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		run  func(*Router) error
		want error
	}{
		{func(r *Router) error { return r.Assign("z", 99) }, ErrPartitionNotFound},
		{func(r *Router) error { return r.Assign("z", 1) }, ErrAffinityConflict},
		{func(r *Router) error { return r.Assign("z", 2) }, ErrAffinityConflict},
		{func(r *Router) error { return r.Assign("a", 0) }, ErrAlreadyAssigned},
		{func(r *Router) error { return r.Put("nobody", "v") }, ErrNoAffinity},
		{func(r *Router) error { return r.Put("d", "x") }, ErrAffinityConflict},
		{func(r *Router) error { return r.Migrate(0, 1) }, ErrInvalidMigrate},
		{func(r *Router) error { return r.Migrate(1, 2) }, ErrInvalidMigrate},
		{func(r *Router) error { return r.Migrate(1, 1) }, ErrInvalidMigrate},
		{func(r *Router) error { return r.Migrate(7, 0) }, ErrPartitionNotFound},
	}
	seen := map[error]bool{}
	for i, tc := range cases {
		r := seed3(t)
		_ = r.Assign("a", 0)
		_ = r.Assign("d", 1)
		_ = r.BeginDrain(1)
		_ = r.BeginDrain(2)
		_ = r.Migrate(2, 0)
		before := [3]int{r.Count(0), r.Count(1), r.Count(2)}
		if err := tc.run(r); !errors.Is(err, tc.want) {
			t.Fatalf("case %d err=%v want %v", i, err, tc.want)
		}
		after := [3]int{r.Count(0), r.Count(1), r.Count(2)}
		if before != after || r.accepted != 2 || r.SelfCheck() != nil || r.Assign("live", 0) != nil {
			t.Fatalf("case %d state changed or unusable", i)
		}
		seen[tc.want] = true
	}
	for _, s := range []error{ErrPartitionNotFound, ErrAffinityConflict, ErrNoAffinity, ErrInvalidMigrate} {
		if !seen[s] {
			t.Fatalf("sentinel %v never exercised", s)
		}
	}
}

func TestSelfCheckInvariants(t *testing.T) {
	for seed := int64(0); seed < 5; seed++ {
		r, rng := New(), rand.New(rand.NewSource(seed))
		for i := 0; i < 8; i++ {
			_ = r.AddPartition(i)
		}
		ids := func(w part.State) (out []int) {
			for id, p := range r.parts {
				if p.State() == w {
					out = append(out, id)
				}
			}
			return out
		}
		for step := 0; step < 200; step++ {
			a, d := ids(part.Active), ids(part.Draining)
			switch rng.Intn(4) {
			case 0:
				if len(a) > 0 {
					_ = r.Assign(fmt.Sprintf("k%d", step), a[rng.Intn(len(a))])
				}
			case 1:
				_ = r.Put("ghost", "v") // rejected: no affinity, no trace
			case 2:
				if len(d) > 0 && len(a) > 0 {
					_ = r.Migrate(d[0], a[rng.Intn(len(a))])
				} else if len(a) > 1 {
					_ = r.BeginDrain(a[0])
				}
			}
			if err := r.SelfCheck(); err != nil {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
		}
		sum := 0
		for id := range r.parts {
			sum += r.Count(id)
		}
		if sum != r.accepted {
			t.Fatalf("seed %d: sum %d != accepted %d", seed, sum, r.accepted)
		}
	}
}

func TestMigrateScanCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			_ = r.AddPartition(i)
			if err := r.Assign(fmt.Sprintf("k%d", i), i); err != nil {
				t.Fatal(err)
			}
		}
		if r.BeginDrain(m-1) != nil || r.Migrate(m-1, 0) != nil {
			t.Fatal("drain/migrate setup")
		}
		if r.lastMigrateScanned != 1 || r.Count(m-1) != 0 || r.Count(0) != 2 {
			t.Fatalf("m=%d scanned=%d counts=%d/%d", m, r.lastMigrateScanned, r.Count(m-1), r.Count(0))
		}
	}
}
