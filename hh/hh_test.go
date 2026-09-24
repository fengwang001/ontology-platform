package hh_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/hh"
)

// state returns snapshot "v@ver|.." (ver 0 = never set) and R1's hint list.
func state(c *hh.Cluster) (string, string) {
	s, g := make([]string, 3), []string{}
	for r, e := range c.Snapshot("k") {
		s[r] = fmt.Sprintf("%s@%d", e.Value, e.Ver)
	}
	for _, h := range c.Hints(1) {
		g = append(g, fmt.Sprintf("%s@%d", h.Value, h.Ver))
	}
	return strings.Join(s, "|"), strings.Join(g, ",")
}
func TestEightStep(t *testing.T) {
	c, _ := hh.New(3, 3)
	c.Down(1)
	W := func(v string, x int64) func() (int, int, error) {
		return func() (int, int, error) { return 0, 0, c.Write("k", v, x) }
	}
	acts := []func() (int, int, error){W("a", 5), W("b", 7), W("c", 6), W("d", 8),
		func() (int, int, error) { return c.Up(1) },
		func() (int, int, error) { _ = c.Down(1); return W("e", 9)() },
		W("f", 9), func() (int, int, error) { return c.Up(1) }}
	wantSnap := []string{"a@5|@0|a@5", "b@7|@0|b@7", "b@7|@0|b@7", "b@7|@0|b@7",
		"b@7|b@7|b@7", "e@9|b@7|e@9", "e@9|b@7|e@9", "e@9|e@9|e@9"}
	wantH := []string{"a@5", "a@5,b@7", "a@5,b@7,c@6", "a@5,b@7,c@6", "", "e@9", "e@9,f@9", ""}
	wantAS := [][2]int{{0, 0}, {0, 0}, {0, 0}, {0, 0}, {2, 1}, {0, 0}, {0, 0}, {1, 1}}
	for i, act := range acts {
		a, s, e := act()
		if a != wantAS[i][0] || s != wantAS[i][1] || errors.Is(e, hh.ErrHintOverflow) != (i == 3) || (i != 3 && e != nil) {
			t.Fatalf("step %d: got (%d,%d,%v), want (%d,%d,overflow=%v)", i+1, a, s, e, wantAS[i][0], wantAS[i][1], i == 3)
		}
		sn, hn := state(c)
		if sn != wantSnap[i] {
			t.Fatalf("step %d snapshot=%q, want %q", i+1, sn, wantSnap[i])
		}
		if hn != wantH[i] {
			t.Fatalf("step %d hints=%q, want %q", i+1, hn, wantH[i])
		}
	}
}
func TestReplayIdempotent(t *testing.T) {
	c, _ := hh.New(2, 2)
	c.Down(0)
	if e := c.Write("k", "a", 1); e != nil {
		t.Fatal(e)
	}
	if a, s, e := c.Up(0); e != nil || a != 1 || s != 0 {
		t.Fatalf("first Up=(%d,%d,%v)", a, s, e)
	}
	if a, s, _ := c.Up(0); a != 0 || s != 0 {
		t.Fatalf("second Up=(%d,%d), want (0,0)", a, s)
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	if hh.ErrConfig == hh.ErrVersion || hh.ErrConfig == hh.ErrKey || hh.ErrConfig == hh.ErrHintOverflow || hh.ErrVersion == hh.ErrKey || hh.ErrVersion == hh.ErrHintOverflow || hh.ErrKey == hh.ErrHintOverflow {
		t.Fatal("the four sentinel errors must be pairwise distinct")
	}
	for _, b := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {2, -1}} {
		if _, e := hh.New(b[0], b[1]); !errors.Is(e, hh.ErrConfig) {
			t.Fatalf("New(%v)=%v", b, e)
		}
	}
	c, _ := hh.New(3, 1)
	c.Down(1)
	c.Write("k", "init", 1)
	before := c.Snapshot("k")
	try := []func() error{
		func() error { return c.Write("", "x", 1) },
		func() error { return c.Write("k", "x", 0) },
		func() error { return c.Write("k", "x", 2) },
		func() error { _, _, e := c.Up(9); return e },
		func() error { return c.Down(-1) },
	}
	want := []error{hh.ErrKey, hh.ErrVersion, hh.ErrHintOverflow, hh.ErrConfig, hh.ErrConfig}
	for i, fn := range try {
		if e := fn(); !errors.Is(e, want[i]) || !reflect.DeepEqual(c.Snapshot("k"), before) {
			t.Fatalf("reject %d: %v (state changed?)", i, e)
		}
	}
	if a, s, _ := c.Up(1); a != 1 || s != 0 {
		t.Fatalf("Up after rejects=(%d,%d)", a, s)
	}
	c.Write("k", "next", 2)
}
func TestConcurrentWrites(t *testing.T) {
	const nK, nW = 16, 6
	a, _ := api.New(3, nK*nW+1)
	a.Down(1)
	var regress int32
	stop := make(chan struct{})
	go func() { // R0 advances only via strictly-newer writes
		last := int64(0)
		for {
			select {
			case <-stop:
				return
			default:
				if v := a.Get("k0")[0].Ver; v < last {
					atomic.StoreInt32(&regress, 1)
				} else if v > last {
					last = v
				}
			}
		}
	}()
	var wg sync.WaitGroup
	wg.Add(nK)
	for i := 0; i < nK; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 1; j <= nW; j++ {
				a.Write(fmt.Sprintf("k%d", i), fmt.Sprintf("%d:%d", i, j), int64(i*10+j))
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	ap, sk, _ := a.Up(1)
	if ap != nK*nW || sk != 0 || atomic.LoadInt32(&regress) != 0 {
		t.Fatalf("Up=(%d,%d) regress=%d, want (%d,0) 0", ap, sk, regress, nK*nW)
	}
	for i := 0; i < nK; i++ {
		for r, g := range a.Get(fmt.Sprintf("k%d", i)) {
			if g.Ver != int64(i*10+nW) || g.Value != fmt.Sprintf("%d:%d", i, nW) {
				t.Fatalf("k%d R%d=%+v", i, r, g)
			}
		}
	}
}
func TestSelfCheck(t *testing.T) {
	a, _ := api.New(3, 3)
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
