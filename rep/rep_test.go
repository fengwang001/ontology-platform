package rep

import (
	"testing"

	"ontology/log"
)

// TestCatchUpCheckedBound：日志 m 条、副本只落后最后一条时，
// catch-up 检查的条目数不随 m 增长（按 lsn 区间直接定位，而非从头扫）。
func TestCatchUpCheckedBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		l := log.New()
		for i := 1; i <= m; i++ {
			l.Append("k", "v")
		}
		r := New()
		r.CatchUp(l, m-1) // 先补到 m-1
		r.CatchUp(l, m)   // 只落后一条，应只检查 1 条
		if r.checked != 1 {
			t.Fatalf("m=%d: checked=%d, want 1（不随 m 线性增长）", m, r.checked)
		}
		if r.Applied() != m {
			t.Fatalf("m=%d: applied=%d, want %d", m, r.Applied(), m)
		}
	}
}

// TestCatchUpClosedInterval：区间是闭的上界 (applied, target]，lsn==target 的条目必须补上。
func TestCatchUpClosedInterval(t *testing.T) {
	l := log.New()
	l.Append("k", "A")
	l.Append("k", "B")
	r := New()
	r.CatchUp(l, 2)
	if got := r.Get("k"); got != "B" {
		t.Fatalf("got %q, want B（闭区间上界必须应用）", got)
	}
	if r.checked != 2 {
		t.Fatalf("checked=%d, want 2", r.checked)
	}
}
