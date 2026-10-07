package batchimport

import (
	"fmt"
	"math"
	"sync"
	"testing"
)

// powersOfTwo 生成 n 条满足 sumPreHook（amount = 前缀和+1）的记录。
func powersOfTwo(n int, label string) []Record {
	out := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Record{
			Type:   typeWidget,
			ID:     fmt.Sprintf("%s%d", label, i+1),
			Fields: mustWidget(int64(math.Pow(2, float64(i))), fmt.Sprintf("%s-l%d", label, i+1)),
		})
	}
	return out
}

// TestPostHookRollsBackWholeBatch 后置钩子失败必须整批撤销。
func TestPostHookRollsBackWholeBatch(t *testing.T) {
	t.Run("post_passes", func(t *testing.T) {
		r := NewRegistry()
		registerTestTypes(r, hookSpec{sumPre: true, capPost: true, postCap: 100})
		records := powersOfTwo(4, "p") // 总和 1+2+4+8=15
		if rep := r.Batch(records, AllOrNothing, 1); !rep.Committed {
			t.Fatalf("依据: 总和 15 <= 100 应提交, got %v", rep.Err)
		}
	})

	t.Run("post_fails_full_rollback", func(t *testing.T) {
		r := NewRegistry()
		registerTestTypes(r, hookSpec{sumPre: true, capPost: true, postCap: 100})
		records := powersOfTwo(10, "q") // 总和 2^10-1 = 1023 > 100
		rep := r.Batch(records, AllOrNothing, 1)
		fmt.Printf("POST-HOOK FAIL INPUT:\n%sACTUAL:\n%s", formatRecords(records), formatReport(rep))
		if rep.Committed || rep.Err == nil {
			t.Fatal("依据: 最终总和 1023 > 100, 后置钩子必须失败")
		}
		if e, ok := AsImportError(rep.Err); !ok || e.Kind != KindPostHook {
			t.Fatalf("依据: 须归一化为批次级后置钩子失败, got %v", rep.Err)
		}
		if len(r.SnapshotType(typeWidget)) != 0 {
			t.Fatalf("依据: 全部 10 条必须一起撤销, 残留 %d 条", len(r.SnapshotType(typeWidget)))
		}
	})

	t.Run("best_effort_never_runs_post_hook", func(t *testing.T) {
		r := NewRegistry()
		registerTestTypes(r, hookSpec{sumPre: true, capPost: true, postCap: 0})
		records := []Record{{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")}}
		rep := r.Batch(records, BestEffort, 1)
		if !rep.Committed || len(r.SnapshotType(typeWidget)) != 1 {
			t.Fatal("依据: 尽力而为语义下不触发批次级后置钩子（cap=0 也不拦截）")
		}
	})
}

// TestErrorPriority 参数非法 > 前置钩子 > 后置钩子，只报第一个命中原因。
func TestErrorPriority(t *testing.T) {
	r := NewRegistry()
	registerTestTypes(r, hookSpec{sumPre: true, capPost: true, postCap: -1})
	records := []Record{
		{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")},
		{Type: typeWidget, ID: "w1", Fields: mustWidget(2, "b")},
	}
	rep := r.Batch(records, AllOrNothing, 1)
	fmt.Printf("PRIORITY INPUT:\n%sACTUAL:\n%s", formatRecords(records), formatReport(rep))
	e, ok := AsImportError(rep.Err)
	if !ok || e.Kind != KindInvalidParam {
		t.Fatalf("依据: 参数非法优先于钩子错误, got %v", rep.Err)
	}

	r2 := NewRegistry()
	registerTestTypes(r2, hookSpec{sumPre: true, capPost: true, postCap: -1})
	bad := []Record{
		{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")},
		{Type: typeWidget, ID: "w2", Fields: map[string]any{"amount": "not-int", "label": "b"}},
	}
	rep2 := r2.Batch(bad, AllOrNothing, 1)
	e2, _ := AsImportError(rep2.Err)
	if e2 == nil || e2.Kind != KindInvalidParam || e2.Index != 1 {
		t.Fatalf("依据: 字段类型不符须归一化为参数非法并定位下标 1, got %v", rep2.Err)
	}

	r3 := NewRegistry()
	registerTestTypes(r3, hookSpec{sumPre: true, capPost: true, postCap: 0})
	good := []Record{{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")}}
	rep3 := r3.Batch(good, AllOrNothing, 1)
	e3, _ := AsImportError(rep3.Err)
	if e3 == nil || e3.Kind != KindPostHook {
		t.Fatalf("依据: 前置通过后后置失败才报批次级错误, got %v", rep3.Err)
	}
	if e.Kind == e3.Kind {
		t.Fatal("依据: KindInvalidParam 与 KindPostHook 必须可区分")
	}
}

// TestConcurrencyDeterminism 同一输入以任意内部并发度执行结果完全一致。
func TestConcurrencyDeterminism(t *testing.T) {
	spec := hookSpec{sumPre: true, uniquePre: true}
	records := []Record{
		{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")},
		{Type: typeWidget, ID: "w2", Fields: mustWidget(2, "b")},
		{Type: typeWidget, ID: "w3", Fields: mustWidget(99, "c")}, // 前置失败，跳过
		{Type: typeWidget, ID: "w4", Fields: mustWidget(4, "d")},  // 前缀和仍为 3，期望 4
		{Type: typeWidget, ID: "w4", Fields: mustWidget(8, "e")},  // 主键重复（参数非法）
		{Type: typeWidget, ID: "w5", Fields: mustWidget(8, "b")},  // 标签 b 可见 -> 唯一钩子失败
	}
	var baseline *Report
	var baselineSnap map[string]Instance
	for _, c := range []int{1, 2, 4, 8, 16} {
		r := NewRegistry()
		registerTestTypes(r, spec)
		rep := r.Batch(records, BestEffort, c)
		snap := r.SnapshotType(typeWidget)
		fmt.Printf("CONCURRENCY=%d\n%s", c, formatReport(rep))
		if baseline == nil {
			baseline, baselineSnap = rep, snap
			continue
		}
		if !reportsEqual(rep, baseline) {
			t.Fatalf("依据: 并发度 %d 的逐条结果与串行基准不一致", c)
		}
		if !snapsEqual(snap, baselineSnap) {
			t.Fatalf("依据: 并发度 %d 的最终状态与串行基准不一致", c)
		}
	}
}

// TestConcurrentBatchesSerializable 并发独立批次等价于某个全局串行顺序。
func TestConcurrentBatchesSerializable(t *testing.T) {
	r := NewRegistry()
	registerTestTypes(r, hookSpec{})
	var wg sync.WaitGroup
	for b := 0; b < 12; b++ {
		wg.Add(1)
		batch := []Record{
			{Type: typeWidget, ID: fmt.Sprintf("b%d-x", b), Fields: mustWidget(int64(b), "x")},
			{Type: typeWidget, ID: fmt.Sprintf("b%d-y", b), Fields: mustWidget(int64(b+1), "y")},
		}
		go func(recs []Record) {
			defer wg.Done()
			if rep := r.Batch(recs, AllOrNothing, 4); !rep.Committed {
				t.Errorf("依据: 不相交主键的并发批次应全部提交, got %v", rep.Err)
			}
		}(batch)
	}
	wg.Wait()
	if got := len(r.SnapshotType(typeWidget)); got != 24 {
		t.Fatalf("依据: 12 批次 x 2 条互不相交, 任意串行化后都应有 24 条, got %d", got)
	}
}

// TestReplayDeterminism 重放同一输入列表得到相同逐条结果与最终状态。
func TestReplayDeterminism(t *testing.T) {
	spec := hookSpec{sumPre: true, uniquePre: true, capPost: true, postCap: 50}
	records := []Record{
		{Type: typeWidget, ID: "w1", Fields: mustWidget(1, "a")},
		{Type: typeWidget, ID: "w2", Fields: mustWidget(2, "b")},
		{Type: typeWidget, ID: "w3", Fields: mustWidget(2, "b")}, // 唯一标签失败
		{Type: typeWidget, ID: "w4", Fields: mustWidget(4, "c")},
		{Type: typeWidget, ID: "w5", Fields: mustWidget(50, "z")}, // 总和超不过也无所谓，先看前置
	}
	var first *Report
	var firstSnap map[string]Instance
	for run := 0; run < 3; run++ {
		r := NewRegistry()
		registerTestTypes(r, spec)
		rep := r.Batch(records, AllOrNothing, 3)
		snap := r.SnapshotType(typeWidget)
		fmt.Printf("REPLAY #%d\n%s", run+1, formatReport(rep))
		if first == nil {
			first, firstSnap = rep, snap
			continue
		}
		if !reportsEqual(rep, first) || !snapsEqual(snap, firstSnap) {
			t.Fatalf("依据: 第 %d 次重放与首次结果/状态不一致", run+1)
		}
	}
}
