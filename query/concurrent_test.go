package query

import (
	"sync"
	"testing"

	"ontology/role"
)

func TestConcurrentOperations(t *testing.T) {
	store := role.NewStore()
	engine := NewEngine(store)
	must(t, store.PutRole("R", []role.Entry{{
		IndexPattern: "ix",
		Fields:       role.FieldAuth{Unrestricted: true},
	}}))
	must(t, store.BindUser("u", []string{"R"}))

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := "d" + itoa((g*200+i)%64)
				_ = engine.PutDoc("ix", id, map[string]role.Value{"level": int64(i % 10)})
				_, _ = engine.Search("u", "ix", role.Range("level", 0, 5), 100)
				_, _ = engine.Agg("u", "ix", "level")
				_, _ = engine.Get("u", "ix", id)
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = store.PutRole("S", []role.Entry{{
				IndexPattern: "*",
				Fields:       role.FieldAuth{Grant: []string{"*"}},
			}})
			_ = store.BindUser("v", []string{"R", "S"})
		}
	}()
	wg.Wait()

	// 串行收尾：最终状态必须自洽（Total 与实际可见文档数一致）。
	res, err := engine.Search("u", "ix", role.Not(role.Term("nope", "x")), 1000)
	must(t, err)
	if res.Total != len(res.Docs) {
		t.Fatalf("final Total=%d len=%d", res.Total, len(res.Docs))
	}
	if res.Total != 64 {
		t.Fatalf("final doc count = %d, want 64", res.Total)
	}
}
