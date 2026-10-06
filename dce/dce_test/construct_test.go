package dce_test

import (
	"strings"
	"testing"

	"ontology/dce/dceerr"
	"ontology/dce/model"
	"ontology/dce/registry"
)

func regErr(t *testing.T, m *model.Module, want dceerr.Category) {
	t.Helper()
	err := registry.New().Register(m)
	if err == nil {
		t.Fatalf("want %s, got nil", want)
	}
	e, ok := dceerr.As(err)
	if !ok || e.Category != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func TestInvalidArguments(t *testing.T) {
	regErr(t, &model.Module{ID: ""}, dceerr.CatInvalidArgument)
	regErr(t, &model.Module{
		ID:    "m",
		Decls: []model.Declaration{{Name: "a"}, {Name: "a"}},
	}, dceerr.CatInvalidArgument)
	regErr(t, &model.Module{
		ID: "m",
		Imports: []model.Import{{
			Target: "x",
			Bindings: []model.ImportBinding{
				{Local: "p", Imported: "a"},
				{Local: "p", Imported: "b"},
			},
		}},
	}, dceerr.CatInvalidArgument)
	regErr(t, &model.Module{
		ID: "m",
		Exports: []model.Export{
			localExp("a"), localExp("a"),
		},
	}, dceerr.CatInvalidArgument)
}

func TestDuplicateModuleRejectedAtomically(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(&model.Module{ID: "m", Decls: []model.Declaration{{Name: "a"}}}); err != nil {
		t.Fatal(err)
	}
	before := len(reg.SnapshotIDs())
	err := reg.Register(&model.Module{ID: "m"})
	if e, ok := dceerr.As(err); !ok || e.Category != dceerr.CatDuplicateModule {
		t.Fatalf("want duplicate module, got %v", err)
	}
	if len(reg.SnapshotIDs()) != before {
		t.Fatalf("rejected registration changed state")
	}
}

func TestUnknownModuleAndUndefinedReference(t *testing.T) {
	// 未知模块优先于未定义引用。
	bad := &model.Module{
		ID: "bad",
		Decls: []model.Declaration{
			{Name: "a", Refs: []string{"ghost"}}, // 未定义引用
		},
		Imports: []model.Import{{Target: "missing"}}, // 未知模块
	}
	app := &model.Module{ID: "app", Decls: []model.Declaration{{Name: "m"}},
		Imports: []model.Import{{Target: "bad"}}, Exports: []model.Export{localExp("m")}}

	reg := registry.New()
	if err := reg.Register(bad); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(app); err != nil {
		t.Fatal(err)
	}
	_, err := reg.NewSession([]string{"app"}).Solve()
	if e, ok := dceerr.As(err); !ok || e.Category != dceerr.CatUnknownModule {
		t.Fatalf("unknown module must win, got %v", err)
	}

	// 移除未知模块后才报未定义引用。
	bad2 := &model.Module{
		ID:    "bad2",
		Decls: []model.Declaration{{Name: "a", Refs: []string{"ghost"}}},
	}
	reg2 := registry.New()
	_ = reg2.Register(bad2)
	_ = reg2.Register(&model.Module{ID: "app2", Decls: []model.Declaration{{Name: "m"}},
		Imports: []model.Import{{Target: "bad2"}}, Exports: []model.Export{localExp("m")}})
	_, err = reg2.NewSession([]string{"app2"}).Solve()
	if e, ok := dceerr.As(err); !ok || e.Category != dceerr.CatUndefinedReference {
		t.Fatalf("want undefined reference, got %v", err)
	}
}

func TestUnknownEntryAtSolve(t *testing.T) {
	reg := registry.New()
	_ = reg.Register(&model.Module{ID: "app", Decls: []model.Declaration{{Name: "m"}}})
	_, err := reg.NewSession([]string{"nope"}).Solve()
	if e, ok := dceerr.As(err); !ok || e.Category != dceerr.CatUnknownModule ||
		!strings.Contains(err.Error(), "entry") {
		t.Fatalf("want unknown entry module, got %v", err)
	}
}
