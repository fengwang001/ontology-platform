package api_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/drift"
)

// naiveClasses 返回朴素单遍重算的逐条分类与最终 last。
func naiveClasses(ws []int64, dt, rt int64) ([]drift.Class, int64) {
	var classes []drift.Class
	last, seen := int64(0), false
	for _, w := range ws {
		var c drift.Class
		switch {
		case !seen:
			last, seen, c = w, true, drift.Normal
		case w > last+dt:
			last, c = w, drift.Drift
		case w >= last:
			last, c = w, drift.Normal
		case last-w <= rt:
			c = drift.Reorder
		default:
			c = drift.Rollback
		}
		classes = append(classes, c)
	}
	return classes, last
}

func randSeq(seed int64) []int64 {
	rng := rand.New(rand.NewSource(seed))
	ws := make([]int64, 200)
	for i := range ws {
		ws[i] = rng.Int63n(200)
	}
	return ws
}

// TestMonotonicLast 钉住不变量 1：回退不改小 last。边界探针各自独立重放。
func TestMonotonicLast(t *testing.T) {
	const dt, rt = int64(10), int64(3)
	for _, ws := range [][]int64{{100, 105, 120, 118, 117, 116}, randSeq(1), randSeq(2), randSeq(3)} {
		_, nlast := naiveClasses(ws, dt, rt)
		newD := func() *api.Detector {
			d, err := api.New(dt, rt)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range ws {
				d.Observe("s", w)
			}
			return d
		}
		if c, err := newD().Observe("s", nlast+dt); err != nil || c != drift.Normal {
			t.Fatalf("probe last+dt = %s,%v, want Normal", c, err)
		}
		if c, err := newD().Observe("s", nlast+dt+1); err != nil || c != drift.Drift {
			t.Fatalf("probe last+dt+1 = %s,%v, want Drift", c, err)
		}
	}
}

// TestNaiveRecompute 钉住不变量 2：分类与朴素重算逐条一致。
func TestNaiveRecompute(t *testing.T) {
	seqs := [][]int64{{100, 105, 120, 118, 117, 116, 130, 141},
		randSeq(10), randSeq(11), randSeq(12)}
	for _, ws := range seqs {
		want, _ := naiveClasses(ws, 10, 3)
		d, _ := api.New(10, 3)
		for i, w := range ws {
			got, err := d.Observe("s", w)
			if err != nil || got != want[i] {
				t.Fatalf("step %d w=%d: %s,%v want %s", i, w, got, err, want[i])
			}
		}
	}
}

// TestCountConservation 钉住不变量 3：三计数各自正确且其和等于非 Normal 总数。
func TestCountConservation(t *testing.T) {
	for seed := int64(0); seed < 5; seed++ {
		d, _ := api.New(7, 2)
		rng := rand.New(rand.NewSource(seed))
		var dc, rc, rbc int64
		for i := 0; i < 400; i++ {
			c, _ := d.Observe("s", rng.Int63n(150))
			switch c {
			case drift.Drift:
				dc++
			case drift.Reorder:
				rc++
			case drift.Rollback:
				rbc++
			}
			gd, gr, grb := d.Counts()
			if gd != dc || gr != rc || grb != rbc || gd+gr+grb != dc+rc+rbc {
				t.Fatalf("seed=%d i=%d: %d/%d/%d sum %d, want %d/%d/%d sum %d",
					seed, i, gd, gr, grb, gd+gr+grb, dc, rc, rbc, dc+rc+rbc)
			}
		}
	}
}

// TestRejectedOpsLeaveNoTrace 钉住不变量 4：三类错误互异可判，被拒后状态不变仍可用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if errors.Is(api.ErrInvalidThreshold, api.ErrEmptySource) ||
		errors.Is(api.ErrEmptySource, api.ErrNegativeWatermark) ||
		errors.Is(api.ErrInvalidThreshold, api.ErrNegativeWatermark) {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	for _, p := range [][2]int64{{0, 0}, {-1, 0}, {1, -1}} {
		if d, err := api.New(p[0], p[1]); !errors.Is(err, api.ErrInvalidThreshold) || d != nil {
			t.Fatalf("New(%v): d=%v err=%v", p, d, err)
		}
	}
	d, _ := api.New(10, 3)
	d.Observe("s", 100)
	b0, b1, b2 := d.Counts()
	for _, tc := range []struct {
		s string
		w int64
		e error
	}{{"", 5, api.ErrEmptySource}, {"s", -1, api.ErrNegativeWatermark}} {
		if c, err := d.Observe(tc.s, tc.w); !errors.Is(err, tc.e) || c != drift.Normal {
			t.Fatalf("Observe(%q,%d): %s,%v", tc.s, tc.w, c, err)
		}
	}
	if c, r, rb := d.Counts(); c != b0 || r != b1 || rb != b2 {
		t.Fatalf("state changed after rejection: %d/%d/%d", c, r, rb)
	}
	if c, _ := d.Observe("s", 110); c != drift.Normal {
		t.Fatalf("post-reject Observe(110) = %s, want Normal", c)
	}
}

// TestSelfCheck：内置自检必须通过。
func TestSelfCheck(t *testing.T) {
	d, err := api.New(10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
