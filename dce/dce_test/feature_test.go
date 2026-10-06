package dce_test

import (
	"reflect"
	"testing"

	"ontology/dce/dceerr"
	"ontology/dce/model"
)

func mustSolve(t *testing.T, g *graph, entries ...string) (map[string]string, []string) {
	t.Helper()
	res, err := g.solve(t, entries...)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	return keptSet(res), res.Included
}

func wantErr(t *testing.T, g *graph, want dceerr.Category, entries ...string) {
	t.Helper()
	_, err := g.solve(t, entries...)
	if err == nil {
		t.Fatalf("want error %s, got nil", want)
	}
	e, ok := dceerr.As(err)
	if !ok || e.Category != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func indexOf(xs []string, x string) int {
	for i, s := range xs {
		if s == x {
			return i
		}
	}
	return -1
}

// 无副作用模块：整体剔除 vs 因被引用而部分保留。
func TestSideEffectFreeModule(t *testing.T) {
	dead := mod("dead", model.SideEffectNo,
		[]model.Declaration{decl("a", true), decl("b", false)}, nil,
		[]model.Export{localExp("a"), localExp("b")})
	util := mod("util", model.SideEffectNo,
		[]model.Declaration{decl("used", false, "helper"), decl("helper", false), decl("unused", true)},
		nil, []model.Export{localExp("used"), localExp("helper"), localExp("unused")})
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "u")},
		[]model.Import{imp("util", bind("u", "used"))},
		[]model.Export{localExp("main")})

	g := newGraph().add(dead).add(util).add(app)
	kept, included := mustSolve(t, g, "app")

	if _, ok := kept["dead/a"]; ok {
		t.Fatalf("side-effect-free unreachable module must be dropped wholly")
	}
	if indexOf(included, "dead") >= 0 {
		t.Fatalf("dead must not be included, got %v", included)
	}
	want := map[string]string{
		"app/main":    "entry-export",
		"util/used":   "referenced",
		"util/helper": "referenced",
	}
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept=%v want=%v", kept, want)
	}
	if _, ok := kept["util/unused"]; ok {
		t.Fatalf("unused side-effectful decl in side-effect-free module must not be kept")
	}
}

// 纯副作用导入引入带副作用目标。
func TestBareSideEffectImport(t *testing.T) {
	poly := mod("poly", model.SideEffectYes,
		[]model.Declaration{decl("install", true), decl("quiet", false)}, nil, nil)
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false)},
		[]model.Import{{Target: "poly"}},
		[]model.Export{localExp("main")})

	kept, included := mustSolve(t, newGraph().add(poly).add(app), "app")
	if kept["poly/install"] != "side-effect" {
		t.Fatalf("poly/install should be side-effect kept, got %v", kept)
	}
	if _, ok := kept["poly/quiet"]; ok {
		t.Fatalf("non-side-effect decl should not be kept: %v", kept)
	}
	if indexOf(included, "poly") < 0 {
		t.Fatalf("poly must be included via bare import")
	}
}

// 通配重导出：同名一致有效；同名不同声明报歧义。
func TestStarReexportAgreeAndAmbiguous(t *testing.T) {
	a := mod("a", model.SideEffectNo,
		[]model.Declaration{decl("x", false)}, nil, []model.Export{localExp("x")})
	b := mod("b", model.SideEffectNo,
		[]model.Declaration{decl("x", false)}, nil, []model.Export{localExp("x")})
	mid := mod("mid", model.SideEffectNo, nil,
		[]model.Import{imp("a"), imp("b")},
		[]model.Export{starExp("a"), starExp("b")})
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "v")},
		[]model.Import{imp("mid", bind("v", "x"))},
		[]model.Export{localExp("main")})
	wantErr(t, newGraph().add(a).add(b).add(mid).add(app), dceerr.CatAmbiguousExport, "app")

	b2 := mod("b2", model.SideEffectNo, nil,
		[]model.Import{imp("a")},
		[]model.Export{namedExp("x", "a", "x")})
	mid2 := mod("mid2", model.SideEffectNo, nil,
		[]model.Import{imp("a"), imp("b2")},
		[]model.Export{starExp("a"), starExp("b2")})
	app2 := mod("app2", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "v")},
		[]model.Import{imp("mid2", bind("v", "x"))},
		[]model.Export{localExp("main")})
	kept, _ := mustSolve(t, newGraph().add(a).add(b2).add(mid2).add(app2), "app2")
	if kept["a/x"] != "referenced" {
		t.Fatalf("agreeing stars should resolve a/x, kept=%v", kept)
	}
}

// 本地导出优先于通配重导出。
func TestLocalExportBeatsStar(t *testing.T) {
	src := mod("src", model.SideEffectNo,
		[]model.Declaration{decl("x", false)}, nil, []model.Export{localExp("x")})
	mid := mod("mid", model.SideEffectNo,
		[]model.Declaration{decl("x", false)}, nil,
		[]model.Export{localExp("x"), starExp("src")})
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "v")},
		[]model.Import{imp("mid", bind("v", "x"))},
		[]model.Export{localExp("main")})
	kept, _ := mustSolve(t, newGraph().add(src).add(mid).add(app), "app")
	if _, ok := kept["mid/x"]; !ok {
		t.Fatalf("local x must win over star, kept=%v", kept)
	}
	if _, ok := kept["src/x"]; ok {
		t.Fatalf("shadowed src/x must not resolve, kept=%v", kept)
	}
}

// default 不被通配重导出/通配导入转发。
func TestDefaultNotForwardedByStar(t *testing.T) {
	src := mod("src", model.SideEffectNo,
		[]model.Declaration{decl("d", false), decl("n", false)}, nil,
		[]model.Export{localExp("default"), localExp("n")})
	mid := mod("mid", model.SideEffectNo, nil, nil,
		[]model.Export{starExp("src")})
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "ns")},
		[]model.Import{imp("mid", bind("ns", "*"))},
		[]model.Export{localExp("main")})
	res, err := newGraph().add(src).add(mid).add(app).solve(t, "app")
	if err != nil {
		t.Fatalf("namespace import must not error on hidden default: %v", err)
	}
	kept := keptSet(res)
	if _, ok := kept["src/d"]; ok {
		t.Fatalf("default must not be forwarded by star, kept=%v", kept)
	}
	if _, ok := kept["src/n"]; !ok {
		t.Fatalf("named export n should be forwarded, kept=%v", kept)
	}
}

// 具名重导出链改名。
func TestNamedReexportRenameChain(t *testing.T) {
	base := mod("base", model.SideEffectNo,
		[]model.Declaration{decl("v", false)}, nil, []model.Export{localExp("v")})
	l1 := mod("l1", model.SideEffectNo, nil, nil,
		[]model.Export{namedExp("a", "base", "v")})
	l2 := mod("l2", model.SideEffectNo, nil, nil,
		[]model.Export{namedExp("b", "l1", "a")})
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "x")},
		[]model.Import{imp("l2", bind("x", "b"))},
		[]model.Export{localExp("main")})
	kept, _ := mustSolve(t, newGraph().add(base).add(l1).add(l2).add(app), "app")
	if kept["base/v"] != "referenced" {
		t.Fatalf("rename chain should resolve base/v, kept=%v", kept)
	}
}

// 通配导入保守保留全部有效声明，歧义名不计入也不报错。
func TestNamespaceImportConservative(t *testing.T) {
	a := mod("a", model.SideEffectNo,
		[]model.Declaration{decl("x", false), decl("y", false)}, nil,
		[]model.Export{localExp("x"), localExp("y")})
	b := mod("b", model.SideEffectNo,
		[]model.Declaration{decl("x", false)}, nil, []model.Export{localExp("x")})
	mid := mod("mid", model.SideEffectNo, nil,
		[]model.Import{imp("a"), imp("b")},
		[]model.Export{starExp("a"), starExp("b")})
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "ns")},
		[]model.Import{imp("mid", bind("ns", "*"))},
		[]model.Export{localExp("main")})
	res, err := newGraph().add(a).add(b).add(mid).add(app).solve(t, "app")
	if err != nil {
		t.Fatalf("namespace import ignores ambiguous names, got %v", err)
	}
	kept := keptSet(res)
	if _, ok := kept["a/y"]; !ok {
		t.Fatalf("a/y must be kept via namespace import, kept=%v", kept)
	}
	if _, ok := kept["a/x"]; ok {
		t.Fatalf("ambiguous a/x must not be counted for namespace import, kept=%v", kept)
	}
}

// 导入环不致死锁且结果正确。
func TestImportCycle(t *testing.T) {
	a := mod("a", model.SideEffectYes,
		[]model.Declaration{decl("fa", false, "fb")},
		[]model.Import{imp("b", bind("fb", "gb"))},
		[]model.Export{localExp("fa")})
	b := mod("b", model.SideEffectYes,
		[]model.Declaration{decl("gb", false, "fa2"), decl("dead", true)},
		[]model.Import{imp("a", bind("fa2", "fa"))},
		[]model.Export{localExp("gb"), localExp("dead")})
	kept, included := mustSolve(t, newGraph().add(a).add(b), "a")
	if indexOf(included, "b") < 0 {
		t.Fatalf("cycle partner b must be included")
	}
	if kept["a/fa"] != "entry-export" || kept["b/gb"] != "referenced" {
		t.Fatalf("cycle closure wrong: %v", kept)
	}
	if kept["b/dead"] != "side-effect" {
		t.Fatalf("side-effect decl in included side-effect module must be kept, got %v", kept)
	}
}

// 重导出循环。
func TestReexportCycle(t *testing.T) {
	a := mod("a", model.SideEffectNo, nil, nil,
		[]model.Export{namedExp("x", "b", "x")})
	b := mod("b", model.SideEffectNo, nil, nil,
		[]model.Export{namedExp("x", "a", "x")})
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "v")},
		[]model.Import{imp("a", bind("v", "x"))},
		[]model.Export{localExp("main")})
	wantErr(t, newGraph().add(a).add(b).add(app), dceerr.CatReexportCycle, "app")
}

// 缺失导出。
func TestMissingExport(t *testing.T) {
	a := mod("a", model.SideEffectNo,
		[]model.Declaration{decl("x", false)}, nil, []model.Export{localExp("x")})
	app := mod("app", model.SideEffectYes,
		[]model.Declaration{decl("main", false, "v")},
		[]model.Import{imp("a", bind("v", "nope"))},
		[]model.Export{localExp("main")})
	wantErr(t, newGraph().add(a).add(app), dceerr.CatMissingExport, "app")
}
