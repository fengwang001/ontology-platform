package lru

import (
	"fmt"
	"testing"
)

func TestOrderAndTouch(t *testing.T) {
	l := New()
	for i, k := range []string{"a", "b", "c"} {
		l.Add(k, int64(i+1))
	}
	if got := l.Keys(); fmt.Sprint(got) != "[c b a]" {
		t.Fatalf("MRU→LRU = %v", got)
	}
	l.Touch("a", 4)
	if got := l.Keys(); fmt.Sprint(got) != "[a c b]" {
		t.Fatalf("after touch = %v", got)
	}
	k, ok := l.LruKey()
	if !ok || k != "b" {
		t.Fatalf("LruKey = %q,%v", k, ok)
	}
	l.Remove(k)
	if got := l.Keys(); fmt.Sprint(got) != "[a c]" || l.Len() != 2 {
		t.Fatalf("after remove = %v len=%d", got, l.Len())
	}
	if _, ok = l.LruKey(); !ok {
		t.Fatal("LruKey on non-empty")
	}
}

// TestEvictChecksConstant：热层键数 m 取多档，定位 LRU 的
// 检查个数不随 m 线性增长（不超过与 m 无关的小常数）。
func TestEvictChecksConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		l := New()
		for i := 0; i < m; i++ {
			l.Add(fmt.Sprintf("k%06d", i), int64(i+1))
		}
		// 模拟触发一次换出前的候选定位
		victim, ok := l.LruKey()
		if !ok || victim != "k000000" {
			t.Fatalf("m=%d victim=%q ok=%v", m, victim, ok)
		}
		if l.lastChecks > 4 {
			t.Fatalf("m=%d lastChecks=%d 随规模增长", m, l.lastChecks)
		}
	}
}
