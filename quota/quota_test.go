package quota

import "testing"

func TestAddAndUsed(t *testing.T) {
	q := New()
	if got := q.Add(0, 100, 3600); got != 100 || q.Used(0) != 100 {
		t.Fatalf("first add: added=%d used=%d", got, q.Used(0))
	}
	if got := q.Add(0, 4000, 3600); got != 3500 {
		t.Fatalf("clamped add: got %d want 3500", got)
	}
	if q.Used(0) != 3600 {
		t.Fatalf("used must not exceed limit: %d", q.Used(0))
	}
	t.Log("入账 100 再入 4000（额度 3600）-> 截断为 3500，Used=3600，不超额")

	if got := q.Add(0, 10, 3600); got != 0 {
		t.Fatalf("full day rejects more: %d", got)
	}
	if got := q.Add(1, 0, 3600); got != 0 {
		t.Fatalf("zero add: %d", got)
	}
	if q.Used(7) != 0 {
		t.Fatal("untouched day must be 0")
	}

	q2 := New()
	if got := q2.Add(3, 50, 0); got != 0 || q2.Used(3) != 0 {
		t.Fatalf("limit 0 must reject all: added=%d used=%d", got, q2.Used(3))
	}
	t.Log("额度为 0 的日：任何入账返回 0（跨入当日即在日界下线）")
}
