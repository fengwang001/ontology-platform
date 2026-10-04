package hold

import "testing"

func TestHeapOrder(t *testing.T) {
	h := New()
	in := []Record{
		{K: 50, Arrival: 1, Payload: "c"},
		{K: 10, Arrival: 2, Payload: "a"},
		{K: 10, Arrival: 3, Payload: "a2"},
		{K: 90, Arrival: 4, Payload: "e"},
		{K: 20, Arrival: 5, Payload: "b"},
	}
	for _, r := range in {
		h.Push(r)
	}
	got, examined := h.DrainLE(20)
	want := []string{"a", "a2", "b"}
	if len(got) != len(want) {
		t.Fatalf("released %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Payload != w {
			t.Fatalf("got[%d]=%v want %s", i, got[i].Payload, w)
		}
	}
	// 释放 3 条：考察堆顶依次为 10,10,20，下一堆顶 50>20 停止 => 4 = 3+1
	if examined != len(got)+1 {
		t.Fatalf("examined=%d released=%d", examined, len(got))
	}
	if h.Len() != 2 {
		t.Fatalf("remaining = %d", h.Len())
	}
	rest := h.DrainAll()
	if len(rest) != 2 || rest[0].Payload != "c" || rest[1].Payload != "e" {
		t.Fatalf("rest = %+v", rest)
	}
}

func TestExaminedIndependentOfPending(t *testing.T) {
	// 待定 100 与 10000 两档，同样释放 3 条，考察数都应为 4。
	for _, total := range []int{100, 10000} {
		h := New()
		// 3 条 k<=300 的记录（插入到大量高 k 记录中间，到达序打乱），其余全部 k>300。
		var arrival int64 = 10
		for i := 0; i < total-3; i++ {
			arrival++
			h.Push(Record{K: int64(400 + i), Arrival: arrival, Payload: i})
			if i == (total-3)/2 {
				h.Push(Record{K: 300, Arrival: 3, Payload: "r3"})
				h.Push(Record{K: 100, Arrival: 1, Payload: "r1"})
				h.Push(Record{K: 200, Arrival: 2, Payload: "r2"})
			}
		}
		if h.Len() != total {
			t.Fatalf("total=%d: heap len %d", total, h.Len())
		}
		out, examined := h.DrainLE(300)
		if len(out) != 3 {
			t.Fatalf("total=%d: released %d", total, len(out))
		}
		if examined != 4 {
			t.Fatalf("total=%d: examined=%d, want 4", total, examined)
		}
		if h.Len() != total-3 {
			t.Fatalf("total=%d: remain %d", total, h.Len())
		}
	}
}

func TestExaminedWhenHeapFullyDrained(t *testing.T) {
	h := New()
	h.Push(Record{K: 1, Arrival: 1})
	h.Push(Record{K: 2, Arrival: 2})
	out, examined := h.DrainLE(10)
	if len(out) != 2 || examined != 2 {
		t.Fatalf("out=%d examined=%d", len(out), examined)
	}
}
