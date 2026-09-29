package firstrow

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func logBatch(t *testing.T, reason string, batch []Change, out []Entry, err error) {
	t.Helper()
	t.Logf("判定依据: %s", reason)
	for i, c := range batch {
		t.Logf("  输入 batch[%d]: %s key=%q id=%q time=%d", i, c.Op, c.Key, c.ID, c.Time)
	}
	if err != nil {
		t.Logf("  结果: 整批拒绝, 原因: %v", err)
		return
	}
	if len(out) == 0 {
		t.Logf("  输出: 无（首条未变化）")
	}
	for _, e := range out {
		t.Logf("  输出条目: %s", e)
	}
}

func TestTieBreakByID(t *testing.T) {
	d := New(0)
	// 排序键 Time 相同（含负值），首条由 ID 字典序最小者胜出。
	batch := []Change{
		{Op: Insert, Key: "k", ID: "b", Time: -5},
		{Op: Insert, Key: "k", ID: "a", Time: -5},
		{Op: Insert, Key: "k", ID: "c", Time: -5},
	}
	out, err := d.Apply(batch)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	logBatch(t, "Time 并列时按 ID 字典序升序取首条", batch, out, nil)

	want := []Entry{
		{Op: Insert, Key: "k", ID: "b", Time: -5},
		{Op: Retract, Key: "k", ID: "b", Time: -5},
		{Op: Insert, Key: "k", ID: "a", Time: -5},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("out = %v, want %v", out, want)
	}
	if id, _, ok := d.First("k"); !ok || id != "a" {
		t.Fatalf("First = %q, %v; want \"a\", true", id, ok)
	}
}

func TestRetractFirstPromotesSecondEarliest(t *testing.T) {
	d := New(0)
	setup := []Change{
		{Op: Insert, Key: "k", ID: "early", Time: -10},
		{Op: Insert, Key: "k", ID: "mid", Time: 3},
		{Op: Insert, Key: "k", ID: "late", Time: 7},
	}
	if _, err := d.Apply(setup); err != nil {
		t.Fatalf("setup Apply: %v", err)
	}

	batch := []Change{{Op: Retract, Key: "k", ID: "early"}}
	out, err := d.Apply(batch)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	logBatch(t, "撤回首条后由次早行 (time=3) 顶上，先撤回旧首条再写入新首条", batch, out, nil)

	want := []Entry{
		{Op: Retract, Key: "k", ID: "early", Time: -10},
		{Op: Insert, Key: "k", ID: "mid", Time: 3},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("out = %v, want %v", out, want)
	}

	// 撤回剩余行后首条清空，只输出撤回。
	batch2 := []Change{
		{Op: Retract, Key: "k", ID: "mid"},
		{Op: Retract, Key: "k", ID: "late"},
	}
	out2, err := d.Apply(batch2)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	logBatch(t, "存活行清空时只撤回旧首条，不写入新首条", batch2, out2, nil)
	want2 := []Entry{
		{Op: Retract, Key: "k", ID: "mid", Time: 3},
		{Op: Insert, Key: "k", ID: "late", Time: 7},
		{Op: Retract, Key: "k", ID: "late", Time: 7},
	}
	if !reflect.DeepEqual(out2, want2) {
		t.Fatalf("out2 = %v, want %v", out2, want2)
	}
	if _, _, ok := d.First("k"); ok {
		t.Fatal("First should be empty after all rows retracted")
	}
}

func TestNoOutputWhenFirstUnchanged(t *testing.T) {
	d := New(0)
	batch := []Change{
		{Op: Insert, Key: "k", ID: "first", Time: 1},
		{Op: Insert, Key: "k", ID: "later", Time: 9},
		{Op: Retract, Key: "k", ID: "later"},
	}
	out, err := d.Apply(batch)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	logBatch(t, "非首条行的写入与撤回不影响首条，不输出", batch, out, nil)
	want := []Entry{{Op: Insert, Key: "k", ID: "first", Time: 1}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("out = %v, want %v", out, want)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name  string
		batch []Change
		want  error
	}{
		{"empty key", []Change{{Op: Insert, Key: "", ID: "a", Time: 1}}, ErrEmptyKey},
		{"empty id", []Change{{Op: Insert, Key: "k", ID: "", Time: 1}}, ErrEmptyID},
		{"duplicate id", []Change{
			{Op: Insert, Key: "k", ID: "a", Time: 1},
			{Op: Insert, Key: "k", ID: "a", Time: 2},
		}, ErrDuplicateID},
		{"retract missing", []Change{{Op: Retract, Key: "k", ID: "ghost"}}, ErrMissingID},
		{"too many rows", []Change{
			{Op: Insert, Key: "k", ID: "a", Time: 1},
			{Op: Insert, Key: "k", ID: "b", Time: 2},
			{Op: Insert, Key: "k", ID: "c", Time: 3},
		}, ErrTooManyRows},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New(2)
			// 预置一条首条，用于验证拒绝后状态与日志均不变。
			pre := []Change{{Op: Insert, Key: "k", ID: "pre", Time: -1}}
			if _, err := d.Apply(pre); err != nil {
				t.Fatalf("pre Apply: %v", err)
			}
			logBefore := d.Log()

			out, err := d.Apply(tc.batch)
			logBatch(t, "非法输入整批拒绝，存活行与日志不变", tc.batch, out, err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.want)
			}
			if out != nil {
				t.Fatalf("rejected batch produced output: %v", out)
			}
			if !reflect.DeepEqual(d.Log(), logBefore) {
				t.Fatalf("log changed after rejection: %v", d.Log())
			}
			id, tm, ok := d.First("k")
			if !ok || id != "pre" || tm != -1 {
				t.Fatalf("First = %q,%d,%v; want pre,-1,true", id, tm, ok)
			}
			if d.LiveCount("k") != 1 {
				t.Fatalf("LiveCount = %d, want 1", d.LiveCount("k"))
			}
		})
	}
}

func TestDeterministicReplay(t *testing.T) {
	batches := [][]Change{
		{
			{Op: Insert, Key: "x", ID: "a", Time: -3},
			{Op: Insert, Key: "x", ID: "b", Time: -3},
			{Op: Insert, Key: "y", ID: "a", Time: 0},
		},
		{
			{Op: Retract, Key: "x", ID: "a"},
			{Op: Insert, Key: "x", ID: "c", Time: -9},
		},
		{
			{Op: Retract, Key: "x", ID: "c"},
			{Op: Retract, Key: "x", ID: "b"},
			{Op: Retract, Key: "y", ID: "a"},
		},
	}
	run := func() []Entry {
		d := New(0)
		var all []Entry
		for _, b := range batches {
			out, err := d.Apply(b)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			all = append(all, out...)
		}
		return all
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs:\n got %v\nwant %v", i, got, first)
		}
	}
	t.Logf("判定依据: 同一输入序列反复计算输出完全一致, 共 %d 条日志", len(first))
	for _, e := range first {
		t.Logf("  输出条目: %s", e)
	}
}

func TestConcurrentReads(t *testing.T) {
	d := New(0)
	if _, err := d.Apply([]Change{{Op: Insert, Key: "k", ID: "a", Time: 1}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if j%2 == 0 {
					_, _ = d.Apply([]Change{
						{Op: Insert, Key: fmt.Sprintf("k%d", i), ID: fmt.Sprintf("id%d", j), Time: int64(j)},
					})
				}
				_ = d.Log()
				_, _, _ = d.First("k")
				_ = d.LiveCount("k")
			}
		}(i)
	}
	wg.Wait()
	t.Logf("判定依据: 并发读写下结果逐键一致, 日志总数 %d", len(d.Log()))
}
