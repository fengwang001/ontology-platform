package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

type spec struct {
	id               string
	lo, hi, vlo, vhi int64
}

var five = []spec{
	{"P0", 0, 10, 10, 20}, {"P1", 10, 20, 30, 40}, {"P2", 20, 30, 50, 60},
	{"P3", 30, 40, 15, 25}, {"P4", 40, 50, 60, 80},
}

func newDB(t *testing.T) *api.DB {
	t.Helper()
	d := api.New()
	for _, f := range five {
		if err := d.AddPartition(f.id, f.lo, f.hi, f.vlo, f.vhi); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// TestSelfCheck drives the public built-in verification of all four
// invariants (soundness, exactness, order independence, no-trace).
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestPublicQuery is table-driven over the §3 fixture and boundary cases.
func TestPublicQuery(t *testing.T) {
	d := newDB(t)
	cases := []struct {
		name               string
		plo, phi, vlo, vhi int64
		wantScan           []string
	}{
		{"fixture", 20, 50, 30, 60, []string{"P2"}},
		{"equality-lo", 10, 50, 30, 60, []string{"P1", "P2"}}, // P0 pruned at hi==Plo; P1 v-hits
		{"equality-hi", 20, 40, 30, 60, []string{"P2"}},       // P4 excluded by lo==Phi
		{"v-open-hi", 20, 50, 30, 50, nil},                    // [..,50) misses all
		{"all-wide", 0, 50, 0, 100, []string{"P0", "P1", "P2", "P3", "P4"}},
	}
	for _, c := range cases {
		scan, pruned, err := d.Query(c.plo, c.phi, c.vlo, c.vhi)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if fmt.Sprint(scan) != fmt.Sprint(c.wantScan) {
			t.Fatalf("%s: scan=%v want=%v pruned=%v", c.name, scan, c.wantScan, pruned)
		}
		if len(scan)+len(pruned) != 5 {
			t.Fatalf("%s: partition not classified", c.name)
		}
	}
}

// TestPublicErrors checks the three pairwise-distinct sentinels through only
// exported calls, and that a rejected add leaves state untouched.
func TestPublicErrors(t *testing.T) {
	d := newDB(t)
	before, _, _ := d.Query(20, 50, 30, 60)
	for _, c := range []struct {
		do   func() error
		want error
	}{
		{func() error { return d.AddPartition("BAD", 10, 5, 1, 2) }, api.ErrInvalidPartition},
		{func() error { return d.AddPartition("OVL", 5, 15, 0, 1) }, api.ErrInvalidPartition},
		{func() error { return d.AddPartition("INV", 0, 5, 9, 8) }, api.ErrInvalidPartition},
		{func() error { _, _, e := d.Query(5, 5, 0, 1); return e }, api.ErrInvalidPredicate},
		{func() error { _, _, e := d.Query(0, 1, 2, 2); return e }, api.ErrInvalidPredicate},
	} {
		if err := c.do(); !errors.Is(err, c.want) {
			t.Fatalf("got %v want %v", err, c.want)
		}
	}
	after, _, _ := d.Query(20, 50, 30, 60)
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("state changed after rejections: %v vs %v", after, before)
	}
	if !errors.Is(api.ErrInvalidPartition, api.ErrInvalidPartition) ||
		errors.Is(api.ErrInvalidPartition, api.ErrInvalidPredicate) ||
		errors.Is(api.ErrInvalidPredicate, api.ErrUnknownPartition) {
		t.Fatal("sentinels not pairwise distinct")
	}
}

// TestConcurrentQueries: N goroutines issue the same Query through the public
// API; every scan set must be element-wise identical. No sleeps.
func TestConcurrentQueries(t *testing.T) {
	d := newDB(t)
	const n = 64
	var wg sync.WaitGroup
	got := make([][]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, _, err := d.Query(20, 50, 30, 60)
			if err != nil {
				t.Errorf("query: %v", err)
				return
			}
			got[i] = s
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if fmt.Sprint(got[0]) != fmt.Sprint(got[i]) {
			t.Fatalf("goroutine %d: %v vs %v", i, got[i], got[0])
		}
	}
}
