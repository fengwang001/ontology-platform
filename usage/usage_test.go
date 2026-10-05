package usage

import (
	"fmt"
	"reflect"
	"testing"
)

// 活跃判定考察的消费者数与历史消费者总数无关：
// 100 与 10000 个早已不活跃的消费者，两档 scanned 增量相同。
func TestScannedIndependentOfHistory(t *testing.T) {
	const q = 30
	var deltas []int
	for _, inactive := range []int{100, 10000} {
		l := New()
		for i := 0; i < inactive; i++ {
			l.RecordAccess("d", fmt.Sprintf("old%05d", i), 1)
		}
		l.RecordAccess("d", "fresh1", 95)
		l.RecordAccess("d", "fresh2", 99)
		before := l.scanned
		got := l.ActiveConsumers("d", 100, q)
		delta := l.scanned - before
		want := []string{"fresh1", "fresh2"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("inactive=%d: got %v want %v", inactive, got, want)
		}
		if bound := len(want) + 1; delta > bound {
			t.Fatalf("inactive=%d: scanned %d exceeds bound %d", inactive, delta, bound)
		}
		deltas = append(deltas, delta)
	}
	if deltas[0] != deltas[1] {
		t.Fatalf("scanned differs across history sizes: %v", deltas)
	}
	t.Logf("scanned delta (100 vs 10000 inactive): %d vs %d", deltas[0], deltas[1])
}

func TestActivitySemantics(t *testing.T) {
	l := New()
	const q = 30
	l.RecordAccess("d", "c", 70)

	// 恰等 now-Q 不算活跃。
	if l.IsActive("d", "c", 100, q) {
		t.Fatal("lastAccess == now-Q must not be active")
	}
	if !l.IsActive("d", "c", 99, q) {
		t.Fatal("lastAccess > now-Q must be active")
	}

	// 确认后不再活跃；确认后的成功访问使确认作废。
	l.Ack("d", "c")
	if l.IsActive("d", "c", 80, q) {
		t.Fatal("acked consumer must not be active")
	}
	l.RecordAccess("d", "c", 75)
	if !l.IsActive("d", "c", 80, q) {
		t.Fatal("access after ack must void the ack")
	}

	// 重复访问只保留最新 lastAccess，次序表中无残留。
	l.RecordAccess("d", "c", 90)
	if got := l.ActiveConsumers("d", 100, q); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("got %v want [c]", got)
	}
	if n := len(l.datasets["d"].order); n != 1 {
		t.Fatalf("order holds %d entries, want 1", n)
	}

	// 无成功访问者不算消费者。
	if l.HasAccess("d", "ghost") {
		t.Fatal("ghost must not have access")
	}
}
