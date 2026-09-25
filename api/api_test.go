package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/snap"
)

func testEvents() []snap.Event {
	return []snap.Event{
		{Pos: 1, Key: "a", Delta: 10}, {Pos: 2, Key: "b", Delta: 20}, {Pos: 3, Key: "a", Delta: 1}, {Pos: 4, Key: "c", Delta: 30},
		{Pos: 5, Key: "b", Delta: 5}, {Pos: 6, Key: "a", Delta: 2}, {Pos: 7, Key: "d", Delta: 40}, {Pos: 8, Key: "b", Delta: 3},
	}
}

// snapshotOnly 只应用 sp 快照、不消费增量，applied 停在 sp。
func snapshotOnly(t *testing.T, events []snap.Event, sp int64) *View {
	v := New()
	if err := v.ApplySnapshot(consistentSnapshot(events, sp)); err != nil {
		t.Fatal(err)
	}
	return v
}

// coldStart 应用 sp 快照并消费全部后续增量。
func coldStart(t *testing.T, events []snap.Event, sp int64) *View {
	v := snapshotOnly(t, events, sp)
	coldRest(t, v, events, sp)
	return v
}

func coldRest(t *testing.T, v *View, events []snap.Event, sp int64) {
	for _, ev := range events {
		if ev.Pos > sp && v.ApplyIncremental(ev) != nil {
			t.Fatalf("增量 pos=%d 失败", ev.Pos)
		}
	}
}

// TestColdStartMatchesNaive 不变量 1：多档 SP 冷启动结果 == 朴素重算。
func TestColdStartMatchesNaive(t *testing.T) {
	events, want := testEvents(), naive(testEvents())
	for sp := int64(0); sp <= int64(len(events)); sp++ {
		t.Run(fmt.Sprintf("sp=%d", sp), func(t *testing.T) {
			if got := coldStart(t, events, sp).State(); !reflect.DeepEqual(got, want) {
				t.Fatalf("sp=%d: got %v want %v", sp, got, want)
			}
		})
	}
}

// TestSwitchNoGapNoOverlap 不变量 2：每个事件恰好计一次（独立 key、Delta=1，最终各 key 恰为 1）。
func TestSwitchNoGapNoOverlap(t *testing.T) {
	var events []snap.Event
	for p := int64(1); p <= 16; p++ {
		events = append(events, snap.Event{Pos: p, Key: fmt.Sprintf("k%d", p), Delta: 1})
	}
	for _, sp := range []int64{0, 1, 7, 15, 16} {
		st := coldStart(t, events, sp).State()
		for p := int64(1); p <= 16; p++ {
			if st[fmt.Sprintf("k%d", p)] != 1 {
				t.Fatalf("sp=%d: 位点 %d 被计的次数 != 1", sp, p)
			}
		}
	}
}

// TestContinuityMonotonic 不变量 3：空洞与回退都被拒，applied 单调不减。
func TestContinuityMonotonic(t *testing.T) {
	events := testEvents()
	v := snapshotOnly(t, events, 4)
	// 空洞（Pos=6 跳过 5）与回退（Pos=4 <= applied）都必须被拒。
	for _, ev := range []snap.Event{{Pos: 6, Key: "x", Delta: 1}, {Pos: 4, Key: "x", Delta: 1}} {
		if err := v.ApplyIncremental(ev); !errors.Is(err, snap.ErrGap) {
			t.Fatalf("pos=%d: want ErrGap got %v", ev.Pos, err)
		}
		if v.Applied() < 4 {
			t.Fatal("applied 出现回退")
		}
	}
	coldRest(t, v, events, 4)
	if v.Applied() != 8 {
		t.Fatalf("applied=%d want 8", v.Applied())
	}
}

// TestRejectedNoSideEffect 不变量 4：三类错误可判定、互不相同、被拒后状态不变。
func TestRejectedNoSideEffect(t *testing.T) {
	v := snapshotOnly(t, testEvents(), 4)
	before, beforeApplied := v.State(), v.Applied()
	cases := []struct {
		name string
		want error
		call func() error
	}{
		{"gap", snap.ErrGap, func() error { return v.ApplyIncremental(snap.Event{Pos: 9, Key: "x", Delta: 1}) }},
		{"badsp", snap.ErrBadSP, func() error { return v.ApplySnapshot(snap.Snapshot{SP: -1}) }},
		{"emptykey", snap.ErrEmptyKey, func() error { return v.ApplyIncremental(snap.Event{Pos: 5, Key: ""}) }},
	}
	for _, c := range cases { // 哨兵错误互不相同由 SelfCheck 与 demo 另行钉住
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); !errors.Is(err, c.want) {
				t.Fatalf("want %v got %v", c.want, err)
			}
			if !reflect.DeepEqual(v.State(), before) || v.Applied() != beforeApplied {
				t.Fatal("被拒操作改变了状态")
			}
		})
	}
	coldRest(t, v, testEvents(), 4) // 拒绝后仍可继续正常使用
	if got := v.State(); !reflect.DeepEqual(got, naive(testEvents())) {
		t.Fatalf("拒绝后继续冷启动结果错误: %v", got)
	}
}

// TestConcurrentView 并发读：N 个 goroutine 反复 View，结果逐 key 相同。
func TestConcurrentView(t *testing.T) {
	v := coldStart(t, testEvents(), 4)
	want := v.State()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if !reflect.DeepEqual(v.State(), want) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("并发 View 结果不一致")
	}
}

// TestSelfCheck 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
