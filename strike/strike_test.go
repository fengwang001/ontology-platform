package strike

import (
	"fmt"
	"testing"
)

// 已过期 100 条与 10000 条两档对照：单次 State 触碰的记录数只取决于
// 此刻有效期内的条数，与已过期历史条数无关。
func TestTouchedIndependentOfExpiredHistory(t *testing.T) {
	const p = int64(10)
	for _, expired := range []int{100, 10000} {
		t.Run(fmt.Sprintf("expired=%d", expired), func(t *testing.T) {
			l := NewLedger(p)
			// 在 t=0..expired-1 各记一条权重 1，t=i 的记录于 i+p 过期。
			for i := 0; i < expired; i++ {
				l.Add(fmt.Sprintf("d%d", i), "u", int64(i), 1)
			}
			now := int64(expired) - 1 // 有效窗口 (now-p, now] 内恰有 p 条
			if got := l.Score("u", now); got != int(p) {
				t.Fatalf("Score = %d, want %d", got, p)
			}
			if got := l.Touched(); got != int(p) {
				t.Fatalf("Touched = %d, want %d（有效条数）", got, p)
			}
			if l.Touched() > int(p)+2 {
				t.Fatalf("Touched = %d 超过有效条数+2", l.Touched())
			}
		})
	}
}

// 触碰数与其他创作者的记录数无关。
func TestTouchedIndependentOfOtherCreators(t *testing.T) {
	l := NewLedger(10)
	for i := 0; i < 5000; i++ {
		l.Add(fmt.Sprintf("x%d", i), "other", int64(i), 2)
	}
	l.Add("a", "u", 5000, 1)
	l.Add("b", "u", 5001, 2)
	if got := l.Score("u", 5001); got != 3 {
		t.Fatalf("Score = %d, want 3", got)
	}
	if got := l.Touched(); got != 2 {
		t.Fatalf("Touched = %d, want 2", got)
	}
}

// 上界：k 条有效（含被推翻但未过期的）记录时，触碰数恰为 k，不超过 k+2。
func TestTouchedBoundWithOverturned(t *testing.T) {
	l := NewLedger(100)
	for i := 0; i < 7; i++ {
		l.Add(fmt.Sprintf("d%d", i), "u", int64(i), 1)
	}
	l.Remove("d2") // 推翻一条：仍被触碰（在时间窗内）但不计入分数
	if got := l.Score("u", 50); got != 6 {
		t.Fatalf("Score = %d, want 6", got)
	}
	if got := l.Touched(); got != 7 {
		t.Fatalf("Touched = %d, want 7", got)
	}
	// 全部过期后触碰 0 条。
	if got := l.Score("u", 1000); got != 0 {
		t.Fatalf("Score = %d, want 0", got)
	}
	if got := l.Touched(); got != 0 {
		t.Fatalf("Touched = %d, want 0", got)
	}
}
