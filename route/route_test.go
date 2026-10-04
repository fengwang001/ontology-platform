package route_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"ontology/route"
)

const n = "13805001234"

// advance pushes the accepted clock to at by registering a harmless, disjoint
// block. It never touches the number under test and lets later QueryAt reach at.
func advance(t *testing.T, r *route.Router, at int64) {
	t.Helper()
	px := "166" + itoa8(int(at)+1)
	if err := r.AssignBlock(px, 11, 9, at); err != nil {
		t.Fatalf("advance setup at %d: %v", at, err)
	}
}

func itoa8(v int) string {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b)
}

func eqPath(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func buildExample(t *testing.T) *route.Router {
	t.Helper()
	r := route.New(50, 90)
	if err := r.AssignBlock("1380", 11, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.AssignBlock("13805", 11, 2, 10); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSpecExample(t *testing.T) {
	r := buildExample(t)
	// Home operator changes when the nested block becomes effective.
	res, err := r.QueryAt(n, 4, route.OR, 9)
	if err != nil || res.Home != 1 || res.Ported || !eqPath(res.Path, []int64{1}) {
		t.Fatalf("t=9 home before nested: %+v err=%v", res, err)
	}
	if _, err := r.RequestPort(n, 2, 3, 69, 20); !errors.Is(err, route.ErrLeadTime) {
		t.Fatalf("at-now=49: %v", err)
	}
	if _, err := r.RequestPort(n, 2, 3, 70, 20); err != nil {
		t.Fatalf("at-now=50 exact: %v", err)
	}
	// Use at=100 port for the rest of the scenario: cancel and re-request.
	if err := r.Cancel(1, 21); err != nil {
		t.Fatal(err)
	}
	id, err := r.RequestPort(n, 2, 3, 100, 22)
	if err != nil || id != 2 {
		t.Fatalf("re-request id=%d err=%v (rejected/cancelled ids are not reused)", id, err)
	}
	advance(t, r, 100)
	res, err = r.QueryAt(n, 4, route.OR, 99)
	if err != nil || !eqPath(res.Path, []int64{2}) || res.Ported {
		t.Fatalf("OR t=99: %+v err=%v", res, err)
	}
	res, _ = r.QueryAt(n, 4, route.OR, 100)
	if !eqPath(res.Path, []int64{2, 3}) || !res.Ported {
		t.Fatalf("OR t=100: %+v", res)
	}
	res, _ = r.QueryAt(n, 4, route.ACQ, 100)
	if !eqPath(res.Path, []int64{3}) {
		t.Fatalf("ACQ t=100: %+v", res)
	}
	res, _ = r.QueryAt(n, 2, route.OR, 100)
	if !eqPath(res.Path, []int64{3}) {
		t.Fatalf("OR orig=home: %+v", res)
	}
	res, _ = r.QueryAt(n, 3, route.OR, 100)
	if len(res.Path) != 0 {
		t.Fatalf("on-net must be empty: %+v", res)
	}
	if err := r.Cancel(id, 100); !errors.Is(err, route.ErrAlreadyEffective) {
		t.Fatalf("cancel at at: %v", err)
	}
}

func TestMultiPortAndPortHome(t *testing.T) {
	must := func(step string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	// Scenario A: multiple ports, then port back to the home operator.
	r := buildExample(t)
	_, e1 := r.RequestPort(n, 2, 3, 100, 20)
	_, e2 := r.RequestPort(n, 3, 1, 250, 150)
	must("p1", e1)
	must("p2", e2)
	advance(t, r, 250)
	res, _ := r.QueryAt(n, 4, route.OR, 250)
	if !eqPath(res.Path, []int64{2, 1}) || !res.Ported {
		t.Fatalf("OR must bypass previous recipients: %+v", res)
	}
	_, e3 := r.RequestPort(n, 1, 2, 320, 260)
	must("p3", e3)
	advance(t, r, 330)
	res, _ = r.QueryAt(n, 4, route.OR, 320)
	if res.Home != 2 || res.Server != 2 || res.Ported || !eqPath(res.Path, []int64{2}) {
		t.Fatalf("port back to home => not ported: %+v", res)
	}

	// Scenario B: a later nested block re-homes a non-ported (then ported)
	// number, so Ported is recomputed from home vs server in real time.
	r2 := buildExample(t)
	_, f1 := r2.RequestPort(n, 2, 3, 100, 20)
	_, f2 := r2.RequestPort(n, 3, 1, 250, 150)
	must("q1", f1)
	must("q2", f2)
	if err := r2.AssignBlock("138050", 11, 4, 300); err != nil {
		t.Fatal(err)
	}
	_, f3 := r2.RequestPort(n, 1, 2, 360, 310)
	must("q3", f3)
	advance(t, r2, 370)
	res, _ = r2.QueryAt(n, 2, route.OR, 310)
	if res.Home != 4 || res.Server != 1 || !eqPath(res.Path, []int64{4, 1}) || !res.Ported {
		t.Fatalf("re-home while served by 1: %+v", res)
	}
	res, _ = r2.QueryAt(n, 1, route.OR, 360)
	if res.Home != 4 || res.Server != 2 || !eqPath(res.Path, []int64{4, 2}) || !res.Ported {
		t.Fatalf("re-home after port back: %+v", res)
	}
}

func TestDisconnectFreezeAndThaw(t *testing.T) {
	r := buildExample(t)
	r.RequestPort(n, 2, 3, 100, 20)
	if err := r.Disconnect(n, 400); err != nil {
		t.Fatal(err)
	}
	advance(t, r, 490)
	for _, tc := range []struct {
		at     int64
		frozen bool
	}{
		{20, false}, {400, true}, {489, true}, {490, false},
	} {
		res, err := r.QueryAt(n, 4, route.OR, tc.at)
		if tc.frozen {
			if !errors.Is(err, route.ErrFrozen) {
				t.Fatalf("t=%d want frozen got %v %+v", tc.at, err, res)
			}
		} else if err != nil || res.Server != 2 || !eqPath(res.Path, []int64{2}) {
			t.Fatalf("t=%d want home service got %v %+v", tc.at, err, res)
		}
	}
	// Query during freeze also reports frozen; thawed Query routes normally.
	res, err := r.Query(n, 4, route.ACQ, 490)
	if err != nil || res.Server != 2 || !eqPath(res.Path, []int64{2}) {
		t.Fatalf("query after thaw: %+v %v", res, err)
	}
	if _, err := r.Query(n, 4, route.ACQ, 450); !errors.Is(err, route.ErrClockRewound) {
		t.Fatalf("frozen query going backwards must be clock-rewound: %v", err)
	}
}

func TestQueryAtClockRules(t *testing.T) {
	r := buildExample(t)
	// t beyond the largest accepted now is invalid and read-only.
	if _, err := r.QueryAt(n, 4, route.ACQ, 11); !errors.Is(err, route.ErrInvalidArgument) {
		t.Fatalf("future QueryAt: %v", err)
	}
	if r.MaxNow() != 10 {
		t.Fatalf("QueryAt advanced clock: %d", r.MaxNow())
	}
	if _, err := r.Query(n, 4, route.ACQ, 30); err != nil {
		t.Fatal(err)
	}
	if r.MaxNow() != 30 {
		t.Fatalf("accepted Query must advance clock: %d", r.MaxNow())
	}
	if _, err := r.Query(n, 4, route.ACQ, 29); !errors.Is(err, route.ErrClockRewound) {
		t.Fatalf("clock rewind via Query: %v", err)
	}
	if _, err := r.QueryAt(n, 0, route.ACQ, 30); !errors.Is(err, route.ErrInvalidArgument) {
		t.Fatalf("bad orig: %v", err)
	}
	if _, err := r.QueryAt(n, 4, route.Method("XX"), 30); !errors.Is(err, route.ErrInvalidArgument) {
		t.Fatalf("bad method: %v", err)
	}
}

func TestProbeBounds(t *testing.T) {
	hot := "13805001234"
	for _, size := range []int{1000, 100000} {
		r := route.New(0, 0)
		if err := r.AssignBlock("1380", 11, 1, 0); err != nil {
			t.Fatal(err)
		}
		// Register many unrelated blocks, each allocating a different number.
		for i := 0; i < size; i++ {
			px := noisePrefix(i)
			if err := r.AssignBlock(px, 11, int64(1+i%4), 0); err != nil {
				t.Fatalf("noise block %d (%s): %v", i, px, err)
			}
		}
		// Build a long, dense history only for the hot number.
		m := 0
		for k := 0; k < 64; k++ {
			donor := int64(1)
			if k > 0 {
				donor = int64(2 + (k-1)%2)
			}
			recipient := int64(2 + k%2)
			now := int64(300 + 2*k)
			if _, err := r.RequestPort(hot, donor, recipient, now, now); err != nil {
				t.Fatalf("hot history build k=%d: %v", k, err)
			}
			m++
		}
		queryNow := int64(300 + 2*64)
		r.Probes()
		res, err := r.Query(hot, 9, route.ACQ, queryNow)
		if err != nil {
			t.Fatal(err)
		}
		bp, hp := r.Probes()
		bound := int64(math.Floor(math.Log2(float64(m)))) + 2
		if bp > int64(len(hot)) {
			t.Fatalf("size=%d block probes=%d > digits %d", size, bp, len(hot))
		}
		if hp > bound {
			t.Fatalf("size=%d history probes=%d > floor(log2(%d))+2=%d", size, hp, m, bound)
		}
		t.Logf("PROBES numbers=%d blockProbes=%d historyProbes=%d (bound log2(%d)+2=%d) input=%s output=%v basis=own-prefixes+binary-search",
			size, bp, hp, m, bound, hot, res.Path)
	}
}

// noisePrefix maps an index to a distinct 3-digit prefix disjoint from 138xxx.
func noisePrefix(i int) string {
	return fmt.Sprintf("%08d", 10_000_000+i) // 8-digit prefixes, disjoint from 138...
}
