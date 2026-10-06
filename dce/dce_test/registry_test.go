package dce_test

import (
	"sync"
	"testing"

	"ontology/dce/analyzer"
	"ontology/dce/model"
	"ontology/dce/registry"
)

// 多调用方并发登记：每个唯一模块恰被接受一次，重复登记被拒绝且无状态污染。
func TestConcurrentRegister(t *testing.T) {
	reg := registry.New()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				id := ids(i)
				_ = reg.Register(&model.Module{
					ID:    id,
					Decls: []model.Declaration{{Name: "d"}},
				})
			}
		}()
	}
	wg.Wait()
	if len(reg.SnapshotIDs()) != 20 {
		t.Fatalf("want 20 accepted unique modules, got %d", len(reg.SnapshotIDs()))
	}
}

func ids(i int) string {
	return "m" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

// 会话基于创建时刻快照：登记后新模块不影响既有会话；外部修改输入不影响登记状态。
func TestSessionSnapshotIsolation(t *testing.T) {
	reg := registry.New()
	_ = reg.Register(&model.Module{ID: "app", Decls: []model.Declaration{{Name: "main"}}})

	sess := reg.NewSession([]string{"app"})
	_ = reg.Register(&model.Module{ID: "later"})

	res, err := sess.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Included) != 1 || res.Included[0] != "app" {
		t.Fatalf("session must be pinned to snapshot at creation, got %v", res.Included)
	}

	// 外部修改已登记对象不影响内部状态（深拷贝）。
	src := &model.Module{ID: "x", Decls: []model.Declaration{{Name: "keep"}}}
	if err := reg.Register(src); err != nil {
		t.Fatal(err)
	}
	src.ID = "mutated"
	if got := reg.SnapshotIDs(); !contains(got, "x") {
		t.Fatalf("external mutation leaked into registry: %v", got)
	}
}

// 多个入口不产生倍增处理：同一（模块,声明）只处理一次。
func TestEntriesDoNotMultiplyWork(t *testing.T) {
	g := newGraph()
	for i := 0; i < 6; i++ {
		id := "e" + string(rune('0'+i))
		g.add(mod(id, model.SideEffectNo,
			[]model.Declaration{decl("d", false)},
			nil, []model.Export{localExp("d")}))
	}
	// 额外共享目标：六个入口都具名导入它。
	shared := mod("shared", model.SideEffectNo,
		[]model.Declaration{decl("v", false)}, nil, []model.Export{localExp("v")})
	g.add(shared)
	for i := 0; i < 6; i++ {
		id := "e" + string(rune('0'+i))
		ex := g.mods[id]
		ex.Imports = []model.Import{imp("shared", bind("v", "v"))}
		ex.Decls[0].Refs = []string{"v"}
	}
	res, err := g.solve(t, "e0", "e1", "e2", "e3", "e4", "e5")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.DeclProcessed != 7 {
		t.Fatalf("each (module,decl) processed once: got %d", res.Stats.DeclProcessed)
	}
	if res.Stats.ModulesScanned != 7 {
		t.Fatalf("each included module scanned once: got %d", res.Stats.ModulesScanned)
	}
	_ = analyzer.ReasonEntry
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
