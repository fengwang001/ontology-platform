package termination

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, n int) *Detector {
	t.Helper()
	d, err := New(n)
	if err != nil {
		t.Fatalf("New(%d): %v", n, err)
	}
	return d
}

func assertQuiescent(t *testing.T, d *Detector, n int) {
	t.Helper()
	snap := d.Snapshot()
	if snap.Pending != 0 {
		t.Fatalf("pending=%d at announcement", snap.Pending)
	}
	for p := 0; p < n; p++ {
		if snap.States[p] != Idle {
			t.Fatalf("process %d active at announcement", p)
		}
	}
	if sum(snap.Counts) != 0 {
		t.Fatalf("count sum=%d at announcement", sum(snap.Counts))
	}
}

func snapEqual(a, b Snapshot) bool {
	if a.Holder != b.Holder || a.Token != b.Token || a.Accum != b.Accum ||
		a.Round != b.Round || a.Pending != b.Pending || len(a.States) != len(b.States) {
		return false
	}
	for i := range a.States {
		if a.States[i] != b.States[i] || a.Colors[i] != b.Colors[i] || a.Counts[i] != b.Counts[i] {
			return false
		}
	}
	return true
}

// ringToInitiator 沿环传递令牌直到回到发起者（每步持有者都必须已空闲），
// 返回 PassToken(0) 的结果。
func ringToInitiator(t *testing.T, d *Detector, n int) (bool, int) {
	t.Helper()
	for p := n - 1; p >= 1; p-- {
		if _, _, err := d.PassToken(p); err != nil {
			t.Fatalf("PassToken(%d): %v", p, err)
		}
	}
	ann, r, err := d.PassToken(0)
	if err != nil {
		t.Fatalf("PassToken(0): %v", err)
	}
	return ann, r
}

// drainToken 从当前持有者开始沿环传递（要求途经者都已空闲），
// 直到 PassToken(0) 返回，适用于令牌停在环中段的场景。
func drainToken(t *testing.T, d *Detector) (bool, int) {
	t.Helper()
	for {
		snap := d.Snapshot()
		h := snap.Holder
		ann, r, err := d.PassToken(h)
		if err != nil {
			t.Fatalf("PassToken(%d): %v", h, err)
		}
		if h == 0 {
			return ann, r
		}
	}
}

func idleEveryProcess(t *testing.T, d *Detector, n int) {
	t.Helper()
	for p := 0; p < n; p++ {
		if err := d.BecomeIdle(p); err != nil && !errors.Is(err, ErrAlreadyIdle) {
			t.Fatalf("BecomeIdle(%d): %v", p, err)
		}
	}
}
