package ontology_test

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/ontology"
)

// TestConcurrentConsistency 用真实并发施压全局串行化：多个 goroutine 同时对
// 同一批实例做旧/新读、旧/新写、删除、回填与声明追加。收敛后校验：
// 每个仍存在的实例，其新旧两种视图在"共有属性"上永远不矛盾，
// 且不存在"已删除却可读到"或"回填/写入同时生效导致的撕裂状态"。
func TestConcurrentConsistency(t *testing.T) {
	s := startBaseStore(t, "c1", "c2", "c3", "c4")

	var wg sync.WaitGroup
	var stop atomic.Bool

	worker := func(fn func(round int)) {
		defer wg.Done()
		for round := 0; ; round++ {
			if stop.Load() {
				return
			}
			fn(round)
		}
	}

	wg.Add(6)
	// 混合旧/新写入。
	go worker(func(r int) {
		id := []string{"c1", "c2", "c3", "c4"}[r%4]
		v := []ontology.Version{ontology.VersionOld, ontology.VersionNew}[r%2]
		_ = s.Write(id, v, ontology.Props{
			"name": ontology.Present("w"),
			"age":  ontology.Present(r),
		})
	})
	// 高频新读未回填/已回填实例：绝不能 panic/撕裂。
	go worker(func(r int) {
		id := []string{"c1", "c2", "c3", "c4"}[r%4]
		_, _ = s.Read(id, ontology.VersionNew)
	})
	// 旧读。
	go worker(func(r int) {
		id := []string{"c1", "c2", "c3", "c4"}[r%4]
		_, _ = s.Read(id, ontology.VersionOld)
	})
	// 回填触发。
	go worker(func(r int) {
		s.RunBackfill()
	})
	// 删除 + 重建，制造"回填期间被删除"窗口。
	go worker(func(r int) {
		id := []string{"c1", "c2", "c3", "c4"}[r%4]
		if err := s.Delete(id); err == nil {
			_ = s.Create(id, ontology.VersionOld, ontology.Props{
				"name": ontology.Present("recreated"),
				"age":  ontology.Present(1),
			})
		}
	})
	// 声明追加（只追加从未生效的新属性）。
	var amendCounter atomic.Int64
	go worker(func(r int) {
		n := amendCounter.Add(1)
		_ = s.AmendDeclaration([]ontology.Mapping{
			{Attr: ontology.AttrName("z" + strconv.FormatInt(n, 10)),
				Kind: ontology.KindAdd, Default: ontology.Present(0)},
		})
	})

	// 让并发跑固定时长（race 下也稳定），然后停止并排空回填。
	time.Sleep(150 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	// 停止后排空回填队列（陈旧项会被识别并跳过）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s.RunBackfill().DidWork {
		}
	}()
	wg.Wait()

	for _, id := range []string{"c1", "c2", "c3", "c4"} {
		ov, oerr := s.Read(id, ontology.VersionOld)
		nv, nerr := s.Read(id, ontology.VersionNew)
		if (oerr == nil) != (nerr == nil) {
			t.Fatalf("%s 两版本存在性不一致: old=%v new=%v", id, oerr, nerr)
		}
		if oerr != nil {
			continue
		}
		for _, shared := range []ontology.AttrName{"name", "age"} {
			ovv, ook := ov[shared]
			nvv, nok := nv[shared]
			if ook != nok || (ook && ovv.Val != nvv.Val) {
				t.Fatalf("%s 共有属性 %s 新旧视图矛盾: old=%v new=%v", id, shared, ovv, nvv)
			}
		}
	}
	t.Log("并发收敛后所有实例新旧视图一致，无撕裂/复活/覆盖")
}
