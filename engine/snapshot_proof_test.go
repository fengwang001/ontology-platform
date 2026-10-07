package engine_test

import (
	"fmt"
	"testing"

	"ontology/engine"
	"ontology/hooks"
	"ontology/naive"
	"ontology/spec"
)

// registerChain 注册一条 depth 层的嵌套调用链，
// 每层写入 writesPerLevel 个对象。
func registerChain(t *testing.T, register func(spec.ActionDef) error,
	depth, writesPerLevel int) {
	t.Helper()
	for level := depth; level >= 1; level-- {
		name := fmt.Sprintf("chain-%d", level)
		body := make([]spec.Op, 0, writesPerLevel+1)
		for w := 0; w < writesPerLevel; w++ {
			body = append(body, wr("item",
				fmt.Sprintf("i-%d-%d", level, w), spec.OpCreate,
				map[string]any{"n": level*1000 + w}))
		}
		if level < depth {
			body = append(body, call(fmt.Sprintf("chain-%d", level+1)))
		}
		if err := register(spec.ActionDef{Name: name, Body: body}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
}

// TestSnapshotCostIndependentOfDepthAndWrites 以可验证的方式证明：
// 判定前置钩子「写入生效前状态」的开销不随事务已嵌套的调用深度
// 或已应用写入总数增长。
//
// 证明方式（ instrumentation 断言 + 对照）：
//  1. 引擎的视图构造是 O(1) 指针包装：ViewCopiedEntries 恒为 0，
//     即构造快照不拷贝、不遍历任何对象；
//  2. 视图构造次数 == 钩子触发次数 + 1（每次前置触发构造一次，
//     提交阶段一次），不存在随深度或写入数增长的隐藏循环；
//  3. 作为对照，朴素模型每次前置钩子都全量深拷贝，
//     SnapshotCopiedEntries 随写入数线性增长。
func TestSnapshotCostIndependentOfDepthAndWrites(t *testing.T) {
	preHookCount := 0
	countingPreHook := func(ctx hooks.Context) error {
		preHookCount++
		return nil
	}

	type row struct {
		depth, writes int
		viewCreated   uint64
		viewCopied    uint64
		naiveCopied   uint64
	}
	var rows []row
	for _, depth := range []int{1, 2, 4, 8, 16} {
		for _, writes := range []int{1, 10, 100} {
			preHookCount = 0
			e := engine.New()
			e.RegisterType("item", spec.Schema{Fields: map[string]spec.FieldType{
				"n": spec.FieldInt,
			}})
			e.RegisterPreHook("item", "count", countingPreHook)
			registerChain(t, func(d spec.ActionDef) error { return e.RegisterAction(d) },
				depth, writes)
			if err := e.Execute("chain-1"); err != nil {
				t.Fatalf("depth=%d writes=%d: %v", depth, writes, err)
			}
			stats := e.StoreStats()

			// 对照：朴素模型执行同一条链。
			m := naive.New()
			m.RegisterType("item", spec.Schema{Fields: map[string]spec.FieldType{
				"n": spec.FieldInt,
			}})
			m.RegisterPreHook("item", "count", func(ctx hooks.Context) error { return nil })
			registerChain(t, m.RegisterAction, depth, writes)
			if err := m.Execute("chain-1"); err != nil {
				t.Fatalf("naive depth=%d writes=%d: %v", depth, writes, err)
			}

			// 断言 1：视图构造零拷贝。
			if stats.ViewCopiedEntries != 0 {
				t.Fatalf("depth=%d writes=%d: 视图构造拷贝了 %d 个条目，应为 0",
					depth, writes, stats.ViewCopiedEntries)
			}
			// 断言 2：视图构造次数 == 前置钩子触发次数 + 1（提交阶段）。
			wantViews := uint64(preHookCount + 1)
			if stats.ViewCreated != wantViews {
				t.Fatalf("depth=%d writes=%d: 视图构造 %d 次，应为 %d（每次钩子触发恰好一次）",
					depth, writes, stats.ViewCreated, wantViews)
			}
			rows = append(rows, row{depth, writes, stats.ViewCreated,
				stats.ViewCopiedEntries, m.SnapshotCopiedEntries})
		}
	}

	t.Log("判定依据：引擎视图构造零拷贝且次数==钩子触发次数+1；朴素模型快照拷贝随规模增长")
	t.Log("depth writes | engine views-created views-copied | naive snapshot-copied")
	for _, r := range rows {
		t.Logf("%5d %6d | %18d %12d | %d", r.depth, r.writes,
			r.viewCreated, r.viewCopied, r.naiveCopied)
	}
	// 朴素模型的快照拷贝必须确实随写入数增长（对照组有效）。
	var first, last row
	for _, r := range rows {
		if r.depth == 16 && r.writes == 1 {
			first = r
		}
		if r.depth == 16 && r.writes == 100 {
			last = r
		}
	}
	if last.naiveCopied <= first.naiveCopied {
		t.Fatalf("对照组失效：朴素模型快照拷贝未随写入数增长（%d -> %d）",
			first.naiveCopied, last.naiveCopied)
	}
}

// BenchmarkViewConstruction 直接测量在不同已应用写入总数下
// 构造一次「写入生效前状态」视图的开销，验证其为常数。
func BenchmarkViewConstruction(b *testing.B) {
	for _, writes := range []int{0, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("writes=%d", writes), func(b *testing.B) {
			e := engine.New()
			e.RegisterType("item", spec.Schema{Fields: map[string]spec.FieldType{
				"n": spec.FieldInt,
			}})
			body := make([]spec.Op, 0, writes)
			for i := 0; i < writes; i++ {
				body = append(body, wr("item", fmt.Sprintf("i-%d", i),
					spec.OpCreate, map[string]any{"n": i}))
			}
			if err := e.RegisterAction(spec.ActionDef{Name: "fill", Body: body}); err != nil {
				b.Fatal(err)
			}
			if err := e.Execute("fill"); err != nil {
				b.Fatal(err)
			}
			// 在一个新事务内测量：视图构造发生在写入应用之后。
			store := e.Store()
			tx := store.Begin(1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tx.View()
			}
			b.StopTimer()
			tx.Rollback()
		})
	}
}
