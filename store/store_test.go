package store

import (
	"errors"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestReleaseChecksO1 证明回收是 O(1)：存活版本数为 m 时，
// 单次 Release 检查过的版本个数是不随 m 增长的小常数。
func TestReleaseChecksO1(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run("", func(t *testing.T) {
			s := New()
			var victim *Handle
			for i := 0; i < m; i++ {
				must(t, s.Publish(i))
				h, err := s.Acquire() // 每个版本留 1 份句柄引用，使其全部存活
				must(t, err)
				if i == 0 {
					victim = h
				}
			}
			if got := s.AliveCount(); got != m {
				t.Fatalf("alive=%d want %d", got, m)
			}
			must(t, victim.Release())
			const maxChecked = 2 // 与 m 无关的小常数：只检查句柄所指那 1 个版本
			if s.lastChecked > maxChecked {
				t.Fatalf("m=%d: checked %d, want <= %d (O(1))", m, s.lastChecked, maxChecked)
			}
			if got := s.AliveCount(); got != m-1 {
				t.Fatalf("after release alive=%d want %d", got, m-1)
			}
		})
	}
}

func TestSharedNotReclaimed(t *testing.T) {
	for _, tc := range []struct{ handles, pubs int }{{1, 1}, {3, 5}, {10, 20}} {
		t.Run("", func(t *testing.T) {
			s := New()
			must(t, s.Publish(10))
			var hs []*Handle
			for i := 0; i < tc.handles; i++ {
				h, err := s.Acquire()
				must(t, err)
				hs = append(hs, h)
			}
			for i := 0; i < tc.pubs; i++ {
				must(t, s.Publish(20+i))
			}
			if got := s.AliveCount(); got != 2 { // v1 + 当前版本
				t.Fatalf("alive=%d want 2", got)
			}
			for _, h := range hs {
				if v, err := h.Get(); err != nil || v != 10 {
					t.Fatalf("shared get=%v,%v want 10,nil", v, err)
				}
				must(t, h.Release())
			}
			if got := s.AliveCount(); got != 1 {
				t.Fatalf("after all released alive=%d want 1", got)
			}
		})
	}
}

func TestImmediateReclaim(t *testing.T) {
	for _, n := range []int{1, 2, 8} {
		t.Run("", func(t *testing.T) {
			s := New()
			must(t, s.Publish(1))
			var hs []*Handle
			for i := 0; i < n; i++ {
				h, err := s.Acquire()
				must(t, err)
				hs = append(hs, h)
			}
			must(t, s.Publish(2))
			for i, h := range hs {
				must(t, h.Release())
				want := 2
				if i == n-1 {
					want = 1 // 最后一份引用释放后必须立即回收
				}
				if got := s.AliveCount(); got != want {
					t.Fatalf("release %d/%d: alive=%d want %d", i+1, n, got, want)
				}
			}
		})
	}
}

func TestFaultInjection(t *testing.T) {
	s := New()
	_, e1 := s.Acquire()
	e2 := s.Publish(-1)
	must(t, s.Publish(1))
	h, err := s.Acquire()
	must(t, err)
	must(t, h.Release())
	e3 := h.Release()
	_, e4 := h.Get()
	errs := []error{e1, e2, e3, e4}
	wants := []error{ErrEmptyStore, ErrNegativeValue, ErrDoubleRelease, ErrUseAfterFree}
	seen := map[error]bool{}
	for i := range errs {
		if !errors.Is(errs[i], wants[i]) || seen[errs[i]] {
			t.Fatalf("fault %d: err=%v want distinct %v", i, errs[i], wants[i])
		}
		seen[errs[i]] = true
	}
	must(t, s.Publish(2)) // 被拒后仍可正常使用
	if _, err := s.Acquire(); err != nil {
		t.Fatal("unusable after faults")
	}
}
