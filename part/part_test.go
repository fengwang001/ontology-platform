package part

import (
	"errors"
	"testing"
)

func TestCommitAndW(t *testing.T) {
	s := NewStore()
	if got := s.W(); got != -1 {
		t.Fatalf("initial W = %d, want -1", got)
	}
	// 乱序提交：W 卡在 -1，直到 0 被提交。
	for _, p := range []int{3, 1, 2} {
		if err := s.Commit(p); err != nil {
			t.Fatalf("Commit(%d) = %v", p, err)
		}
		if got := s.W(); got != -1 {
			t.Fatalf("W = %d, want -1", got)
		}
	}
	if err := s.Commit(0); err != nil {
		t.Fatalf("Commit(0) = %v", err)
	}
	if got := s.W(); got != 3 {
		t.Fatalf("W = %d, want 3", got)
	}
	if err := s.Commit(1); !errors.Is(err, ErrAlready) {
		t.Fatalf("re-Commit(1) = %v, want ErrAlready", err)
	}
	if got := s.Ver(9); got != 0 {
		t.Fatalf("Ver(9) = %d, want 0 (Missing)", got)
	}
}

// probes 上界：一次 Commit 的 probes 不超过 (新W − 旧W) + 1。100 与 100000 两档。
func TestProbesBound(t *testing.T) {
	for _, n := range []int{100, 100000} {
		t.Run("", func(t *testing.T) {
			s := NewStore()
			// 顺序提交：每次 W 恰好前进 1，probes 增量不超过 2。
			for p := 0; p < n; p++ {
				oldW, oldProbes := s.W(), s.Probes()
				if err := s.Commit(p); err != nil {
					t.Fatalf("Commit(%d) = %v", p, err)
				}
				if got, want := s.Probes()-oldProbes, int64(s.W()-oldW)+1; got > want {
					t.Fatalf("Commit(%d): probes delta %d > (W delta)+1 = %d", p, got, want)
				}
			}
			if got := s.W(); got != n-1 {
				t.Fatalf("W = %d, want %d", got, n-1)
			}
			if got := s.Probes(); got > 2*int64(n) {
				t.Fatalf("total probes %d > 2*%d", got, n)
			}
		})
	}
}

// 逆序提交：前 n-1 次 W 不动（各 1 次探测），最后一次 W 从 -1 跳到 n-1。
func TestProbesReverseOrder(t *testing.T) {
	const n = 1000
	s := NewStore()
	for p := n - 1; p >= 1; p-- {
		oldProbes := s.Probes()
		if err := s.Commit(p); err != nil {
			t.Fatalf("Commit(%d) = %v", p, err)
		}
		if got := s.Probes() - oldProbes; got != 1 {
			t.Fatalf("Commit(%d): probes delta = %d, want 1", p, got)
		}
		if got := s.W(); got != -1 {
			t.Fatalf("W = %d, want -1", got)
		}
	}
	oldProbes := s.Probes()
	if err := s.Commit(0); err != nil {
		t.Fatalf("Commit(0) = %v", err)
	}
	if got, want := s.Probes()-oldProbes, int64(n+1); got != want {
		t.Fatalf("final probes delta = %d, want %d", got, want)
	}
	if got := s.W(); got != n-1 {
		t.Fatalf("W = %d, want %d", got, n-1)
	}
}

func TestBump(t *testing.T) {
	s := NewStore()
	if err := s.Commit(0); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(2); err != nil {
		t.Fatal(err)
	}
	pre := s.Bump(1, 3)
	want := []int{0, 1, 0}
	for i := range want {
		if pre[i] != want[i] {
			t.Fatalf("pre[%d] = %d, want %d", i, pre[i], want[i])
		}
	}
	for p, v := range map[int]int{0: 1, 1: 1, 2: 2, 3: 1} {
		if got := s.Ver(p); got != v {
			t.Fatalf("Ver(%d) = %d, want %d", p, got, v)
		}
	}
	if got := s.W(); got != 3 {
		t.Fatalf("W = %d, want 3", got)
	}
	// 版本只增不减。
	pre = s.Bump(2, 2)
	if pre[0] != 2 || s.Ver(2) != 3 {
		t.Fatalf("pre = %v, Ver(2) = %d, want pre 2 then ver 3", pre, s.Ver(2))
	}
}
