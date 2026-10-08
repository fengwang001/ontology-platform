package ontology_test

import (
	"reflect"
	"testing"

	"ontology"
)

func mustGraph(t *testing.T) *ontology.Graph {
	t.Helper()
	g, err := ontology.NewGraph([]ontology.ObjectTypeSpec{
		{ID: "T1"},
		{ID: "T2"},
		{ID: "TF", ForbiddenInPathQuery: true},
	}, []ontology.Category{"cA", "cB", "cC"})
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func registerTypes(t *testing.T, g *ontology.Graph, specs ...ontology.LinkTypeSpec) {
	t.Helper()
	for _, s := range specs {
		must(t, g.RegisterLinkType(s))
	}
}

func addObjects(t *testing.T, g *ontology.Graph, typ ontology.ObjectTypeID, ids ...ontology.ObjectID) {
	t.Helper()
	for _, id := range ids {
		must(t, g.AddObject(id, typ))
	}
}

func addLink(t *testing.T, g *ontology.Graph, id ontology.LinkID, typ ontology.LinkTypeID, from, to ontology.ObjectID) {
	t.Helper()
	must(t, g.AddLink(id, typ, from, to))
}

func query(t *testing.T, g *ontology.Graph, q ontology.PathQuery) ontology.PathResult {
	t.Helper()
	res, err := g.Query(q)
	if err != nil {
		t.Fatalf("Query(%+v): %v", q, err)
	}
	return res
}

func cat(c ontology.Category) ontology.PatternElem { return ontology.PatternElem{Category: c} }
func star(c ontology.Category) ontology.PatternElem {
	return ontology.PatternElem{Category: c, Star: true}
}
func anyElem() ontology.PatternElem { return ontology.PatternElem{Any: true} }

// buildChain 构造测试图：s -cA-> a -cA-> b -cB-> t（各代价 1），
// 外加 s -cB-> t 直达（代价 10）。
func buildChain(t *testing.T) *ontology.Graph {
	t.Helper()
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "LA", From: "T1", To: "T1", Category: "cA", Cost: 1},
		ontology.LinkTypeSpec{ID: "LB", From: "T1", To: "T1", Category: "cB", Cost: 1},
		ontology.LinkTypeSpec{ID: "LB10", From: "T1", To: "T1", Category: "cB", Cost: 10},
	)
	addObjects(t, g, "T1", "s", "a", "b", "t")
	addLink(t, g, "l1", "LA", "s", "a")
	addLink(t, g, "l2", "LA", "a", "b")
	addLink(t, g, "l3", "LB", "b", "t")
	addLink(t, g, "l4", "LB10", "s", "t")
	return g
}

func TestStarMarkerCombinations(t *testing.T) {
	g := buildChain(t)

	t.Run("exact length mismatch is not a match", func(t *testing.T) {
		// 存在的类别序列是 [cA cA cB] 与 [cB]，都不是恰好 [cA cB]。
		res := query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{cat("cA"), cat("cB")}})
		if res.Found {
			t.Fatalf("expected no path, got %+v", res)
		}
	})

	t.Run("leading star allows extra repetitions", func(t *testing.T) {
		res := query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{star("cA"), cat("cB")}})
		if !res.Found {
			t.Fatal("expected a path")
		}
		if res.Cost != 3 {
			t.Fatalf("cost = %d, want 3", res.Cost)
		}
		if want := []ontology.ObjectID{"s", "a", "b", "t"}; !reflect.DeepEqual(res.Objects, want) {
			t.Fatalf("objects = %v, want %v", res.Objects, want)
		}
		if want := []ontology.Category{"cA", "cA", "cB"}; !reflect.DeepEqual(res.Categories, want) {
			t.Fatalf("categories = %v, want %v", res.Categories, want)
		}
	})

	t.Run("trailing star still requires leading element", func(t *testing.T) {
		// [cA cB*]：直达 [cB] 首元素不匹配，[cA cA cB] 第二位不是 cB。
		res := query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{cat("cA"), star("cB")}})
		if res.Found {
			t.Fatalf("expected no path, got %+v", res)
		}
	})

	t.Run("both ends starred", func(t *testing.T) {
		res := query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{star("cA"), star("cB")}})
		if !res.Found || res.Cost != 3 {
			t.Fatalf("expected cost 3 path, got %+v", res)
		}
	})

	t.Run("single starred element repeats", func(t *testing.T) {
		res := query(t, g, ontology.PathQuery{Start: "s", End: "b", Pattern: []ontology.PatternElem{star("cA")}})
		if !res.Found || res.Cost != 2 {
			t.Fatalf("expected cost 2 path, got %+v", res)
		}
	})

	t.Run("zero length path allowed when pattern matches empty", func(t *testing.T) {
		res := query(t, g, ontology.PathQuery{Start: "s", End: "s", Pattern: []ontology.PatternElem{star("cA")}})
		if !res.Found || res.Cost != 0 {
			t.Fatalf("expected zero-cost path, got %+v", res)
		}
		if want := []ontology.ObjectID{"s"}; !reflect.DeepEqual(res.Objects, want) {
			t.Fatalf("objects = %v, want %v", res.Objects, want)
		}
	})

	t.Run("zero length path rejected when pattern needs a link", func(t *testing.T) {
		res := query(t, g, ontology.PathQuery{Start: "s", End: "s", Pattern: []ontology.PatternElem{cat("cA")}})
		if res.Found {
			t.Fatalf("expected no path, got %+v", res)
		}
	})
}

func TestTieBreakCategoryOrder(t *testing.T) {
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "LA", From: "T1", To: "T1", Category: "cA", Cost: 1},
		ontology.LinkTypeSpec{ID: "LB", From: "T1", To: "T1", Category: "cB", Cost: 1},
		ontology.LinkTypeSpec{ID: "LC", From: "T1", To: "T1", Category: "cC", Cost: 1},
	)
	addObjects(t, g, "T1", "s", "x", "y", "t")
	addLink(t, g, "l1", "LA", "s", "x")
	addLink(t, g, "l2", "LC", "x", "t")
	addLink(t, g, "l3", "LB", "s", "y")
	addLink(t, g, "l4", "LA", "y", "t")

	// 两条候选代价都是 2：[cA cC] 与 [cB cA]；类别全序 cA<cB<cC，
	// [cA cC] 字典序更小。
	res := query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{anyElem(), anyElem()}})
	if !res.Found {
		t.Fatal("expected a path")
	}
	if want := []ontology.Category{"cA", "cC"}; !reflect.DeepEqual(res.Categories, want) {
		t.Fatalf("categories = %v, want %v", res.Categories, want)
	}
	if want := []ontology.ObjectID{"s", "x", "t"}; !reflect.DeepEqual(res.Objects, want) {
		t.Fatalf("objects = %v, want %v", res.Objects, want)
	}
	if res.Equivalent {
		t.Fatal("paths are not equivalent")
	}
}

func TestTieBreakObjectOrder(t *testing.T) {
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "LA", From: "T1", To: "T1", Category: "cA", Cost: 1},
	)
	// 故意先注册字典序更大的对象，验证结果与插入顺序无关。
	addObjects(t, g, "T1", "s", "t", "obj-b", "obj-a")
	addLink(t, g, "l1", "LA", "s", "obj-b")
	addLink(t, g, "l2", "LA", "obj-b", "t")
	addLink(t, g, "l3", "LA", "s", "obj-a")
	addLink(t, g, "l4", "LA", "obj-a", "t")

	res := query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{cat("cA"), cat("cA")}})
	if !res.Found {
		t.Fatal("expected a path")
	}
	if want := []ontology.ObjectID{"s", "obj-a", "t"}; !reflect.DeepEqual(res.Objects, want) {
		t.Fatalf("objects = %v, want %v", res.Objects, want)
	}
}

func TestEquivalentPaths(t *testing.T) {
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "LA", From: "T1", To: "T1", Category: "cA", Cost: 1},
	)
	addObjects(t, g, "T1", "s", "t")
	// 同一对象对上的重复同类型链接：两条路径代价、类别序列、对象序列
	// 完全相同，仅链接实例不同。
	addLink(t, g, "l1", "LA", "s", "t")
	addLink(t, g, "l2", "LA", "s", "t")

	q := ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{cat("cA")}}
	first := query(t, g, q)
	if !first.Found || !first.Equivalent {
		t.Fatalf("expected equivalent paths, got %+v", first)
	}
	if want := []ontology.LinkID{"l1"}; !reflect.DeepEqual(first.Links, want) {
		t.Fatalf("links = %v, want canonical %v", first.Links, want)
	}
	// 结果必须是确定性的，不得随机选择。
	for i := 0; i < 10; i++ {
		if got := query(t, g, q); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d: got %+v, want %+v", i, got, first)
		}
	}
}
