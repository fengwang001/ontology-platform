package dedupe

import (
	"errors"
	"fmt"
	"testing"
)

func TestCapacityBounds(t *testing.T) {
	if _, err := New[int](0); !errors.Is(err, ErrBadCapacity) {
		t.Fatalf("k=0: err=%v, want ErrBadCapacity", err)
	}
	if _, err := New[int](MaxCapacity + 1); !errors.Is(err, ErrBadCapacity) {
		t.Fatalf("k=1e6+1: err=%v, want ErrBadCapacity", err)
	}
	if _, err := New[int](1); err != nil {
		t.Fatalf("k=1: %v", err)
	}
	if _, err := New[int](MaxCapacity); err != nil {
		t.Fatalf("k=1e6: %v", err)
	}
	t.Log("容量边界：0 与 1e6+1 拒绝，1 与 1e6 接受")
}

func TestFIFOEviction(t *testing.T) {
	tb, _ := New[string](2)
	tb.Add("a", "A")
	tb.Add("b", "B")
	// Hit on "a" must not refresh its position.
	if _, ok := tb.Get("a"); !ok {
		t.Fatal("a missing")
	}
	tb.Add("c", "C") // evicts a, not b
	t.Logf("满表加入 c 后：a=%v b=%v c=%v（命中不刷新，淘汰最旧的 a）",
		tb.Contains("a"), tb.Contains("b"), tb.Contains("c"))
	if tb.Contains("a") || !tb.Contains("b") || !tb.Contains("c") {
		t.Fatal("oldest entry a should be evicted")
	}
	if tb.Len() != 2 {
		t.Fatalf("Len=%d, want 2", tb.Len())
	}
}

func TestEvictedUidIsNewAgain(t *testing.T) {
	tb, _ := New[int](1)
	tb.Add("x", 1)
	tb.Add("y", 2) // evicts x
	if tb.Contains("x") {
		t.Fatal("x should be evicted")
	}
	tb.Add("x", 3) // x is a fresh registration, evicts y
	if tb.Contains("y") {
		t.Fatal("y should be evicted by re-registered x")
	}
	if v, _ := tb.Get("x"); v != 3 {
		t.Fatalf("x=%d, want 3", v)
	}
	t.Log("被淘汰的 uid 再登记视为新项，并参与后续淘汰")
}

func TestBoundedUnderChurn(t *testing.T) {
	const k = 100
	tb, _ := New[int](k)
	for i := 0; i < 100_000; i++ {
		tb.Add(fmt.Sprintf("u%d", i), i)
		if tb.Len() > k {
			t.Fatalf("Len=%d exceeds k=%d", tb.Len(), k)
		}
	}
	for i := 99_900; i < 100_000; i++ {
		if !tb.Contains(fmt.Sprintf("u%d", i)) {
			t.Fatalf("recent uid u%d missing", i)
		}
	}
	t.Log("10 万次登记后表大小仍 <= K，且保留最近 K 项")
}
