package gc

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/vv"
)

func chkErr(t *testing.T, got, want error) {
	t.Helper()
	failIf(t, !errors.Is(got, want), "err=%v want %v", got, want)
}

// TestStableMonotonic 不变量2：任何操作后 Stable 逐分量不降。
func TestStableMonotonic(t *testing.T) {
	for seed := int64(0); seed < 8; seed++ {
		rng, R := rand.New(rand.NewSource(seed)), 1+int(seed%3)
		s := mustStore(R)
		before := s.Stable()
		for i := 0; i < 200; i++ {
			v, k, rr := rv(rng, R), fmt.Sprintf("k%d", rng.Intn(5)), rng.Intn(R)
			if n := rng.Intn(3); n == 0 {
				_ = s.Write(rr, k, "x", v)
			} else if n == 1 {
				_ = s.Delete(rr, k, v)
			} else {
				_ = s.Sync(rr, v)
			}
			for j, x := range s.Stable() {
				failIf(t, x < before[j], "seed%d comp%d", seed, j)
			}
			before = s.Stable()
		}
	}
}

// TestGCSafety 不变量3：GC 只回收 vec<=Stable 的墓碑；旧事件不复活，更新事件可重写。
func TestGCSafety(t *testing.T) {
	s := mustStore(2)
	_ = s.Write(0, "a", "1", tv(2, 0))
	_ = s.Delete(0, "a", tv(3, 0))
	_ = s.Sync(1, tv(3, 0))
	failIf(t, !reflect.DeepEqual(s.GC(), []string{"a"}), "GC want [a]")
	for _, w := range []vv.Vector{tv(0, 9), tv(3, 0), tv(2, 2)} {
		_ = s.Write(1, "a", "z", w)
		_, ok := s.View()["a"]
		failIf(t, ok, "stale %v resurrected a", w)
	}
	_ = s.Write(0, "a", "new", tv(4, 0))
	e := s.View()["a"]
	failIf(t, e.Val != "new" || e.Tomb, "greater write: %+v", e)
}

// TestRejectedLeavesNoTrace 不变量4：四类哨兵互异；单条/批量被拒整体不变、之后仍可用。
func TestRejectedLeavesNoTrace(t *testing.T) {
	_, e0 := New(0)
	chkErr(t, e0, ErrInvalidR)
	s := mustStore(2)
	_ = s.Write(0, "k", "v", tv(1, 0))
	snap := func() string { return fmt.Sprintf("%v|%v|%v", s.clk, s.store, s.Stable()) }
	before := snap()
	calls := []struct {
		n    string
		want error
		f    func() error
	}{
		{"replica", ErrReplica, func() error { return s.Write(2, "q", "z", tv(0, 0)) }},
		{"neg", ErrVector, func() error { return s.Delete(0, "q", tv(-1, 0)) }},
		{"emptykey", ErrKey, func() error { return s.Write(0, "", "z", tv(2, 0)) }},
	}
	seen := map[error]bool{ErrInvalidR: true}
	for _, c := range calls {
		e := c.f()
		chkErr(t, e, c.want)
		failIf(t, seen[e], "%s not distinct", c.n)
		failIf(t, snap() != before, "%s left a trace", c.n)
		seen[e] = true
	}
	chkErr(t, s.Sync(0, tv(1)), ErrVector)
	failIf(t, snap() != before, "bad-length left a trace")
	bad := []Op{{KindWrite, 0, "q", "z", tv(2, 0)}, {KindWrite, 9, "q", "z", tv(2, 0)}}
	chkErr(t, s.Apply(bad), ErrReplica)
	failIf(t, snap() != before, "bad batch left a trace")
	chkErr(t, s.Write(0, "k2", "v2", tv(2, 0)), nil)
}

// TestGCProbeCount 复杂度：不可回收墓碑下 probeN<=常数；推过恰好1条后 <=1+常数。
func TestGCProbeCount(t *testing.T) {
	const C = 5
	for _, m := range []int{100, 1000, 10000} {
		s := mustStore(2)
		for i := 1; i <= m; i++ {
			_ = s.Delete(0, fmt.Sprintf("k%d", i), tv(1, i))
		}
		failIf(t, len(s.GC()) != 0 || s.probeN > C, "m=%d probe=%d", m, s.probeN)
		_ = s.Sync(1, tv(1, 1))
		r := s.GC()
		failIf(t, len(r) != 1 || r[0] != "k1" || s.probeN > 1+C, "m=%d %v probe=%d", m, r, s.probeN)
	}
}

// TestConcurrentViews 并发只读：N 个 goroutine 的 View 逐字段相同（-race 干净）。
func TestConcurrentViews(t *testing.T) {
	s := mustStore(3)
	for i := 0; i < 60; i++ {
		_ = s.Write(i%3, fmt.Sprintf("k%d", i), "v", tv(i%4, (i*2)%4, (i*3)%4))
	}
	want, wg := s.View(), sync.WaitGroup{}
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				failIf(t, !reflect.DeepEqual(s.View(), want), "concurrent View differs")
				_ = s.Stable()
			}
		}()
	}
	wg.Wait()
}
