package scope

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"ontology/sym"
)

// TestNaiveReference pins invariant 1 against an innermost-first model.
func TestNaiveReference(t *testing.T) {
	for iter := 0; iter < 100; iter++ {
		rng := rand.New(rand.NewSource(int64(iter)))
		got, want := New(), newNaive()
		for k := 0; k < 200; k++ {
			name := string(rune('a' + rng.Intn(6)))
			ty := []sym.T{sym.Int, sym.Bool}[rng.Intn(2)]
			switch rng.Intn(4) {
			case 0:
				got.Enter()
				want.enter()
			case 1:
				if g, w := got.Exit(), want.exit(); g != w {
					t.Fatalf("Exit %v vs %v", g, w)
				}
			case 2:
				if g, w := got.Declare(name, ty), want.declare(name, ty); g != w {
					t.Fatalf("Declare %s %v vs %v", name, g, w)
				}
			default:
				gv, ge := got.Lookup(name)
				wv, we := want.lookup(name)
				if ge != we || gv != wv {
					t.Fatalf("Lookup %s (%v,%v) vs (%v,%v)", name, gv, ge, wv, we)
				}
			}
		}
	}
}

// TestShadowingRestored pins invariant 2.
func TestShadowingRestored(t *testing.T) {
	for _, tc := range [][2]sym.T{{sym.Int, sym.Bool}, {sym.Bool, sym.Int}} {
		s := New()
		_ = s.Declare("x", tc[0])
		s.Enter()
		if e := s.Declare("x", tc[1]); e != nil {
			t.Fatal(e)
		}
		if v, _ := s.Lookup("x"); v != tc[1] {
			t.Fatalf("inner %v want %v", v, tc[1])
		}
		_ = s.Exit()
		if v, _ := s.Lookup("x"); v != tc[0] {
			t.Fatalf("outer %v want %v", v, tc[0])
		}
	}
}

// TestDuplicateKeepsFirst pins invariant 3.
func TestDuplicateKeepsFirst(t *testing.T) {
	for _, tc := range [][2]sym.T{{sym.Int, sym.Bool}, {sym.Bool, sym.Int}} {
		s := New()
		_ = s.Declare("z", tc[0])
		if e := s.Declare("z", tc[1]); !errors.Is(e, ErrDuplicate) {
			t.Fatalf("second = %v want ErrDuplicate", e)
		}
		if v, _ := s.Lookup("z"); v != tc[0] {
			t.Fatalf("kept %v want %v", v, tc[0])
		}
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	ops := []struct {
		name string
		do   func(*Stack) error
	}{
		{"duplicate", func(s *Stack) error { return s.Declare("x", sym.Bool) }},
		{"undeclared", func(s *Stack) error { _, e := s.Lookup("nope"); return e }},
		{"exit-global", func(s *Stack) error { return s.Exit() }},
	}
	for _, tc := range ops {
		s := New()
		_ = s.Declare("x", sym.Int)
		depth, snap := s.Depth(), s.Snapshot()
		if e := tc.do(s); e == nil {
			t.Fatalf("%s: want error", tc.name)
		}
		if s.Depth() != depth || !reflect.DeepEqual(s.Snapshot(), snap) {
			t.Fatalf("%s: state changed %d->%d", tc.name, depth, s.Depth())
		}
	}
}

// TestProbeCountConstant pins section 4: probes stay constant as m grows.
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			_ = s.Declare("n"+strconv.Itoa(i), sym.Int)
		}
		target := "n" + strconv.Itoa(m-1)
		// Hit lookup and dup check each locate by key, independent of m.
		d := s.probeDelta(func() {
			if v, e := s.Lookup(target); e != nil || v != sym.Int {
				t.Fatalf("lookup m=%d", m)
			}
			if e := s.Declare(target, sym.Bool); !errors.Is(e, ErrDuplicate) {
				t.Fatalf("dup m=%d", m)
			}
		})
		if d > 2 {
			t.Fatalf("m=%d probes %d, want <= 2", m, d)
		}
	}
}

// naive is the reference model: scan innermost-out, first hit wins.
type naive struct{ sc []map[string]sym.T }

func newNaive() *naive  { return &naive{[]map[string]sym.T{{}}} }
func (n *naive) enter() { n.sc = append(n.sc, map[string]sym.T{}) }
func (n *naive) exit() error {
	if len(n.sc) == 1 {
		return ErrExitGlobal
	}
	n.sc = n.sc[:len(n.sc)-1]
	return nil
}
func (n *naive) declare(name string, t sym.T) error {
	top := n.sc[len(n.sc)-1]
	if _, ok := top[name]; ok {
		return ErrDuplicate
	}
	top[name] = t
	return nil
}
func (n *naive) lookup(name string) (sym.T, error) {
	for i := len(n.sc) - 1; i >= 0; i-- {
		if t, ok := n.sc[i][name]; ok {
			return t, nil
		}
	}
	return 0, ErrUndeclared
}
