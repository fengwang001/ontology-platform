package freelist

import (
	"math/rand"
	"testing"

	"ontology/slot"
)

func TestTakeGiveFIFO(t *testing.T) {
	l := New(make([]slot.Slot, 3))
	cases := []struct {
		give bool // true=还 arg 号槽位，false=取一个并期望 want
		arg  int
		want int
	}{
		{false, 0, 0},
		{false, 0, 1},
		{true, 0, -1}, // 还回 0 号：尾插到队尾
		{false, 0, 2}, // 先拿到 2 而不是刚还的 0
		{false, 0, 0}, // 最后才轮到 0
	}
	for _, c := range cases {
		if c.give {
			l.Give(c.arg)
			continue
		}
		_, got, ok := l.Take()
		if !ok || got != c.want {
			t.Errorf("Take = %d,%v, want %d", got, ok, c.want)
		}
	}
	if _, _, ok := l.Take(); ok {
		t.Error("Take on empty list should fail")
	}
	if l.Len() != 0 {
		t.Errorf("Len = %d, want 0", l.Len())
	}
}

// 取一个空闲槽位访问的槽位记录数不得随容量增长（链表而非扫描）。
func TestTakeVisitsScaling(t *testing.T) {
	for _, capN := range []int{1000, 100000} {
		l := New(make([]slot.Slot, capN))
		for i := 0; i < capN; i++ {
			if _, _, ok := l.Take(); !ok {
				t.Fatalf("cap %d: Take %d failed", capN, i)
			}
		}
		perm := rand.New(rand.NewSource(1)).Perm(capN)
		for _, i := range perm[:capN/2] {
			l.Give(i)
		}
		if _, _, ok := l.Take(); !ok {
			t.Fatalf("cap %d: final Take failed", capN)
		}
		if l.lastVisits > 4 {
			t.Errorf("cap %d: visited %d slot records, want <= 4", capN, l.lastVisits)
		}
	}
}
