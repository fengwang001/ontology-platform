package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/snap"
)

var events = []snap.Event{
	{Pos: 1, Key: "a", Delta: 10}, {Pos: 2, Key: "b", Delta: 20},
	{Pos: 3, Key: "a", Delta: 1}, {Pos: 4, Key: "c", Delta: 30},
	{Pos: 5, Key: "b", Delta: 5}, {Pos: 6, Key: "a", Delta: 2},
	{Pos: 7, Key: "d", Delta: 40}, {Pos: 8, Key: "b", Delta: 3},
}

func naiveReplay() map[string]int64 {
	m := map[string]int64{}
	for _, ev := range events {
		m[ev.Key] += ev.Delta
	}
	return m
}

func coldStart(t *testing.T, sp int64) *View {
	t.Helper()
	table := map[string]int64{}
	for _, ev := range events {
		if ev.Pos <= sp {
			table[ev.Key] += ev.Delta
		}
	}
	v := New()
	if err := v.ApplySnapshot(snap.Snapshot{SP: sp, Table: table}); err != nil {
		t.Fatalf("ApplySnapshot SP=%d: %v", sp, err)
	}
	for _, ev := range events {
		if ev.Pos > sp {
			if err := v.ApplyIncremental(ev); err != nil {
				t.Fatalf("ApplyIncremental pos=%d: %v", ev.Pos, err)
			}
		}
	}
	return v
}

// TestColdStartMatchesNaive 不变量 1：任意切换位点的冷启动 == 朴素重算。
func TestColdStartMatchesNaive(t *testing.T) {
	want := naiveReplay()
	for sp := int64(0); sp <= int64(len(events)); sp++ {
		t.Run(fmt.Sprintf("SP=%d", sp), func(t *testing.T) {
			if got := coldStart(t, sp).State(); !equalMap(got, want) {
				t.Fatalf("state=%v, want %v", got, want)
			}
		})
	}
}

// TestNoGapNoOverlap 不变量 2：每个事件恰好计一次，最终 applied 覆盖末位点。
func TestNoGapNoOverlap(t *testing.T) {
	for sp := int64(0); sp <= int64(len(events)); sp++ {
		v := coldStart(t, sp)
		if v.Applied() != int64(len(events)) {
			t.Fatalf("SP=%d: applied=%d, want %d", sp, v.Applied(), len(events))
		}
		// 逐 key 一致即「每个事件恰好一次」的直接推论（累加可交换）。
		if got := v.State(); !equalMap(got, naiveReplay()) {
			t.Fatalf("SP=%d: state=%v", sp, got)
		}
	}
}

// TestRejectsNonContiguous 不变量 3：空洞与回退都被拒绝，applied 单调。
func TestRejectsNonContiguous(t *testing.T) {
	cases := []struct {
		name string
		pos  int64
	}{
		{"gap ahead", 6},
		{"replay current", 4},
		{"rewind", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := coldStart(t, 4)
			err := v.ApplyIncremental(snap.Event{Pos: tc.pos, Key: "x", Delta: 1})
			if !errors.Is(err, snap.ErrGap) {
				t.Fatalf("pos=%d: got %v, want ErrGap", tc.pos, err)
			}
			if v.Applied() != 8 {
				t.Fatalf("applied moved to %d", v.Applied())
			}
		})
	}
}

// TestConcurrentView 并发反复 View，各自拿到的 map 必须逐 key 相同。
func TestConcurrentView(t *testing.T) {
	v := coldStart(t, 4)
	want := naiveReplay()
	const n = 16
	results := make([]map[string]int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				results[i] = v.State()
			}
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if !equalMap(got, want) {
			t.Fatalf("goroutine %d: state=%v, want %v", i, got, want)
		}
	}
}

// TestSelfCheck 自检方法本身必须通过，且可并发调用。
func TestSelfCheck(t *testing.T) {
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = SelfCheck()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("SelfCheck goroutine %d: %v", i, err)
		}
	}
}
