package tpart

import (
	"reflect"
	"testing"

	"ontology/tbucket"
)

// 不变量 2：floor 除法对负时间戳正确。
func TestFloorDiv(t *testing.T) {
	cases := []struct{ a, b, want int64 }{
		{-12, 10, -2}, {-1, 10, -1}, {-10, 10, -1}, {-11, 10, -2}, {-20, 10, -2},
		{0, 10, 0}, {9, 10, 0}, {10, 10, 1}, {19, 10, 1},
		{-12, 3, -4}, {-13, 3, -5}, {7, 1, 7}, {-7, 1, -7}, {-1, 1, -1},
	}
	for _, c := range cases {
		if got := tbucket.FloorDiv(c.a, c.b); got != c.want {
			t.Errorf("FloorDiv(%d,%d)=%d, want %d", c.a, c.b, got, c.want)
		}
		if !tbucket.Contains(c.want, c.a, c.b) {
			t.Errorf("Contains(%d,%d,%d)=false", c.want, c.a, c.b)
		}
	}
}

// 第三节八步分步表：逐步核对落桶计数与 Dropped。
func TestEightSteps(t *testing.T) {
	seq := []int64{5, -12, 0, 10, -1, 20, 9, 30}
	wantCount := []int64{1, 1, 2, 1, 1, 1, 3, 1}
	wantDrop := []int64{0, 0, 0, 1, 1, 2, 2, 5}
	p, err := New(10, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i, ts := range seq {
		if err := p.Feed([]Event{{TS: ts, Key: "k"}}); err != nil {
			t.Fatal(err)
		}
		k := tbucket.Key(ts, 10)
		if got := p.View()["k"][k]; got != wantCount[i] {
			t.Errorf("step %d: bucket %d count=%d, want %d", i+1, k, got, wantCount[i])
		}
		if got := p.Dropped(); got != wantDrop[i] {
			t.Errorf("step %d: Dropped=%d, want %d", i+1, got, wantDrop[i])
		}
	}
	want := map[string]map[int64]int64{"k": {1: 1, 2: 1, 3: 1}}
	if !reflect.DeepEqual(p.View(), want) {
		t.Errorf("final view=%v, want %v", p.View(), want)
	}
	// (丙)：迟到事件落入已清理的桶，直接丢弃且不重开。
	if err := p.Feed([]Event{{TS: -5, Key: "k"}}); err != nil {
		t.Fatal(err)
	}
	if p.Dropped() != 6 || !reflect.DeepEqual(p.View(), want) {
		t.Errorf("late event: Dropped=%d view=%v, want 6 and %v", p.Dropped(), p.View(), want)
	}
}

// 复杂度约束：清理检查过的桶数不随未清理桶数 m 线性增长。
func TestCleanupCheckedIsConstant(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		p, err := New(1, m) // R=m：窗口覆盖全部桶，无清理
		if err != nil {
			t.Fatal(err)
		}
		evs := make([]Event, m)
		for i := range evs {
			evs[i] = Event{TS: int64(i), Key: "k"}
		}
		if err := p.Feed(evs); err != nil {
			t.Fatal(err)
		}
		if err := p.Feed([]Event{{TS: m, Key: "k"}}); err != nil { // cur 前进 1，清桶 0
			t.Fatal(err)
		}
		if p.checked > 3 {
			t.Errorf("m=%d: checked=%d, want <=3（与 m 无关的小常数）", m, p.checked)
		}
		if p.Dropped() != 1 {
			t.Errorf("m=%d: Dropped=%d, want 1", m, p.Dropped())
		}
	}
}

// 不变量 4：失败不留痕；三类哨兵错误互不相同。
func TestRejectionLeavesState(t *testing.T) {
	if _, err := New(0, 3); err != ErrBadSize {
		t.Errorf("New(0,3) err=%v, want ErrBadSize", err)
	}
	if _, err := New(10, 0); err != ErrBadR {
		t.Errorf("New(10,0) err=%v, want ErrBadR", err)
	}
	if ErrBadSize == ErrBadR || ErrBadR == ErrEmptyKey || ErrBadSize == ErrEmptyKey {
		t.Fatal("sentinel errors must be distinct")
	}
	p, _ := New(10, 3)
	if err := p.Feed([]Event{{5, "k"}, {10, "k"}}); err != nil {
		t.Fatal(err)
	}
	before, beforeDrop := p.View(), p.Dropped()
	err := p.Feed([]Event{{40, "k"}, {50, ""}, {60, "k"}}) // 中间一条空 Key，整批拒收
	if err != ErrEmptyKey {
		t.Fatalf("Feed err=%v, want ErrEmptyKey", err)
	}
	if !reflect.DeepEqual(p.View(), before) || p.Dropped() != beforeDrop {
		t.Error("rejected batch changed state")
	}
	if err := p.Feed([]Event{{20, "k"}}); err != nil { // 之后仍可正常使用
		t.Fatalf("usable after rejection: %v", err)
	}
}
