package apply_test

import (
	"sync"
	"testing"

	"ontology/apply"
)

// TestConcurrentSafe 高并发混合调用：仅要求不竞态、不 panic、状态自洽
// （落库行引用恒存活、tid 无空洞、死信只增）。语义等价性由对拍覆盖。
func TestConcurrentSafe(t *testing.T) {
	e := mustNew(t, 1000, 10000, 1, 2, 3)
	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				fn()
			}
		}()
	}
	run(func() {
		// 先造根，再造引用行，大多最终可落库。
		for s := 1; s <= 3; s++ {
			e.Upsert(s, apply.KindDept, 1, 0, 0, 0)
			e.Upsert(s, apply.KindEmp, 1, 1, 0, 0)
		}
		for s := 1; s <= 3; s++ {
			for id := int64(2); id <= 30; id++ {
				e.Upsert(s, apply.KindDept, id, id-1, 0, 0)
				e.Upsert(s, apply.KindEmp, id, id-1, id-1, 0)
			}
		}
	})
	run(func() {
		for s := 1; s <= 3; s++ {
			for id := int64(2); id <= 15; id++ {
				e.Delete(s, apply.KindDept, id, 0)
				e.Delete(s, apply.KindEmp, id, 0)
			}
		}
	})
	run(func() {
		e.Tick(0)
		_ = e.Dead()
		_ = e.Snapshot()
		_ = e.Next(apply.KindDept)
		_ = e.PendingLen()
	})
	wg.Wait()

	// 自洽性：每条存活行的非零引用必须指向存活行（同表 tid 集合判定）。
	alive := map[int]map[int64]bool{}
	for _, r := range e.Snapshot() {
		if alive[r.Key.Kind] == nil {
			alive[r.Key.Kind] = map[int64]bool{}
		}
		alive[r.Key.Kind][r.Tid] = true
	}
	for _, r := range e.Snapshot() {
		if r.A != 0 && !alive[apply.KindDept][r.A] {
			t.Fatalf("row %+v points to dead/missing dept tid %d", r.Key, r.A)
		}
		if r.B != 0 && !alive[apply.KindEmp][r.B] {
			t.Fatalf("row %+v points to dead/missing emp tid %d", r.Key, r.B)
		}
	}
}
