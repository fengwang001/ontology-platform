package table

import (
	"errors"
	"slices"
	"testing"

	"ontology/handle"
	"ontology/slot"
)

func mustNew(t *testing.T, capacity int) *Table {
	t.Helper()
	tab, err := New(capacity)
	if err != nil {
		t.Fatalf("New(%d): %v", capacity, err)
	}
	return tab
}

func TestNewBadCap(t *testing.T) {
	for _, capacity := range []int{0, -1, -100} {
		if _, err := New(capacity); !errors.Is(err, ErrBadCap) {
			t.Fatalf("New(%d) = %v；期望 ErrBadCap", capacity, err)
		}
	}
}

func TestStaleNeverRevives(t *testing.T) {
	tab := mustNew(t, 1)
	h, _ := tab.Insert("v0")
	_ = tab.Remove(h)
	for i := 0; i < 100; i++ { // 槽位反复复用，老句柄每次都不得通过
		nh, err := tab.Insert(i)
		if err != nil {
			t.Fatal(err)
		}
		_ = tab.Remove(nh)
		if _, err := tab.Get(h); !errors.Is(err, ErrStale) {
			t.Fatalf("第%d次复用后老句柄 Get = %v；期望 ErrStale", i, err)
		}
	}
	if tab.Len() != 0 {
		t.Fatalf("Len = %d；期望 0", tab.Len())
	}
}

func TestDistinctErrors(t *testing.T) {
	ta, tb := mustNew(t, 2), mustNew(t, 2)
	ha, _ := ta.Insert("a")
	hb, _ := tb.Insert("b")
	stale, _ := ta.Insert("s")
	_ = ta.Remove(stale)
	cases := []struct {
		name string
		h    handle.Handle
		want error
	}{
		{"零值句柄", handle.Handle(0), ErrZeroHandle},
		{"跨表句柄", hb, ErrWrongTable},
		{"已失效句柄", stale, ErrStale},
		{"越界槽位", handle.Make(ha.TableID(), 1, 99), ErrStale},
	}
	for _, c := range cases {
		if _, err := ta.Get(c.h); !errors.Is(err, c.want) {
			t.Fatalf("%s：Get = %v；期望 %v", c.name, err, c.want)
		}
		if err := ta.Remove(c.h); !errors.Is(err, c.want) {
			t.Fatalf("%s：Remove = %v；期望 %v", c.name, err, c.want)
		}
	}
	all := []error{ErrBadCap, ErrTableFull, ErrZeroHandle, ErrWrongTable, ErrStale, ErrSlotExhausted}
	for i, e1 := range all {
		for _, e2 := range all[i+1:] {
			if errors.Is(e1, e2) {
				t.Fatalf("哨兵错误 %v 与 %v 不可区分", e1, e2)
			}
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	tab, other := mustNew(t, 2), mustNew(t, 1)
	h1, _ := tab.Insert(1)
	_, _ = tab.Insert(2)
	stale, _ := other.Insert(0)
	_ = other.Remove(stale)
	views0, free0 := tab.Inspect()
	rejects := []func() error{
		func() error { _, err := tab.Insert(3); return err },  // 满：ErrTableFull
		func() error { _, err := tab.Get(0); return err },     // 零值：ErrZeroHandle
		func() error { _, err := tab.Get(stale); return err }, // 跨表：ErrWrongTable
		func() error { // 代号不符：ErrStale
			return tab.Remove(handle.Make(h1.TableID(), h1.Gen()+1, h1.Slot()))
		},
	}
	for i, op := range rejects {
		if err := op(); err == nil {
			t.Fatalf("第%d个被拒操作应返回错误", i)
		}
	}
	views1, free1 := tab.Inspect()
	if !slices.Equal(views0, views1) || !slices.Equal(free0, free1) || tab.Len() != 2 {
		t.Fatal("被拒操作改变了表的可观测状态")
	}
}
func TestStaleTwiceSameError(t *testing.T) {
	tab := mustNew(t, 1)
	h, _ := tab.Insert("x")
	_ = tab.Remove(h)
	_, e1 := tab.Get(h)
	_, e2 := tab.Get(h)
	if !errors.Is(e1, ErrStale) || !errors.Is(e2, ErrStale) {
		t.Fatalf("两次 Get = %v, %v；期望均为 ErrStale", e1, e2)
	}
}

func TestSlotExhaustion(t *testing.T) {
	tab := mustNew(t, 3)
	h0, _ := tab.Insert("x")
	h1, _ := tab.Insert("y")
	h2, _ := tab.Insert("z")
	_ = tab.Remove(h0)
	reused, _ := tab.Insert("w") // 复用槽位 0
	last := tab.pushGenToMax(0)  // 测试钩子：代号快进到上限
	if _, err := tab.Get(reused); !errors.Is(err, ErrStale) {
		t.Fatalf("快进后旧句柄 Get = %v；期望 ErrStale", err)
	}
	if v, err := tab.Get(last); err != nil || v != "w" {
		t.Fatalf("末代句柄 Get = %v,%v；期望 w,nil", v, err)
	}
	_ = tab.Remove(last)
	if _, err := tab.Get(last); !errors.Is(err, ErrSlotExhausted) {
		t.Fatalf("退役后末代句柄 Get = %v；期望 ErrSlotExhausted", err)
	}
	views, free := tab.Inspect()
	if views[0].State != slot.Exhausted || len(free) != 0 {
		t.Fatalf("槽位0状态=%v 空闲链表=%v；期望 Exhausted 且不在链表", views[0].State, free)
	}
	if tab.Len() != 2 || tab.Len()+len(free)+1 != tab.Cap() {
		t.Fatal("退役后不变量3等式不成立")
	}
	if v, err := tab.Get(h1); err != nil || v != "y" { // 整表其余部分仍可用
		t.Fatalf("存活句柄 Get = %v,%v", v, err)
	}
	_ = tab.Remove(h2)
	if _, err := tab.Insert("q"); err != nil {
		t.Fatalf("退役一个槽位后 Insert = %v；期望 nil", err)
	}
}
