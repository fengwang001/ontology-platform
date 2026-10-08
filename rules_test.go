package ontology_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology"
)

// buildIsolated 构造 s -cA-> m -cA-> t（代价 1），另有 s -cA-> t 直达（代价 5）。
func buildIsolated(t *testing.T) *ontology.Graph {
	t.Helper()
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "LA", From: "T1", To: "T1", Category: "cA", Cost: 1},
		ontology.LinkTypeSpec{ID: "LA5", From: "T1", To: "T1", Category: "cA", Cost: 5},
	)
	addObjects(t, g, "T1", "s", "m", "t")
	addLink(t, g, "l1", "LA", "s", "m")
	addLink(t, g, "l2", "LA", "m", "t")
	addLink(t, g, "l3", "LA5", "s", "t")
	return g
}

func TestIsolatedObjectCannotBeIntermediate(t *testing.T) {
	g := buildIsolated(t)
	must(t, g.SetIsolation("m", true))
	q := ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{star("cA")}}

	// 隔离对象不得作为中间节点：只能走代价 5 的直达链接。
	res := query(t, g, q)
	if !res.Found || res.Cost != 5 {
		t.Fatalf("expected cost 5 direct path, got %+v", res)
	}
	if want := []ontology.ObjectID{"s", "t"}; !reflect.DeepEqual(res.Objects, want) {
		t.Fatalf("objects = %v, want %v", res.Objects, want)
	}

	// 取消隔离后应恢复经过 m 的更优路径。
	must(t, g.SetIsolation("m", false))
	res = query(t, g, q)
	if !res.Found || res.Cost != 2 {
		t.Fatalf("expected cost 2 path via m, got %+v", res)
	}
}

func TestIsolatedObjectAsEndpoint(t *testing.T) {
	g := buildIsolated(t)
	must(t, g.SetIsolation("m", true))

	// 隔离对象作为终点：可直接查询其自身的直连关系。
	res := query(t, g, ontology.PathQuery{Start: "s", End: "m", Pattern: []ontology.PatternElem{star("cA")}})
	if !res.Found || res.Cost != 1 {
		t.Fatalf("expected cost 1 path to isolated endpoint, got %+v", res)
	}

	// 隔离对象作为起点。
	res = query(t, g, ontology.PathQuery{Start: "m", End: "t", Pattern: []ontology.PatternElem{star("cA")}})
	if !res.Found || res.Cost != 1 {
		t.Fatalf("expected cost 1 path from isolated start, got %+v", res)
	}

	// 隔离对象作为中间节点仍然被禁止。
	res = query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{cat("cA"), cat("cA")}})
	if res.Found {
		t.Fatalf("isolated object must not be an intermediate node, got %+v", res)
	}
}

// buildPermission 构造：s -cA(sec)-> t（代价 1，仅 admin 可见），
// s -cA(pub)-> x -cA(pub)-> t（代价 2，公开），y 仅经 sec 与 s 相连。
func buildPermission(t *testing.T) *ontology.Graph {
	t.Helper()
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "SEC", From: "T1", To: "T1", Category: "cA", Cost: 1, RestrictedTo: []ontology.Principal{"admin"}},
		ontology.LinkTypeSpec{ID: "PUB", From: "T1", To: "T1", Category: "cA", Cost: 1},
	)
	addObjects(t, g, "T1", "s", "x", "t", "y")
	addLink(t, g, "l-sec", "SEC", "s", "t")
	addLink(t, g, "l-pub-1", "PUB", "s", "x")
	addLink(t, g, "l-pub-2", "PUB", "x", "t")
	addLink(t, g, "l-sec-y", "SEC", "s", "y")
	return g
}

func TestPermissionInvisibleLinksCauseReroute(t *testing.T) {
	g := buildPermission(t)
	pat := []ontology.PatternElem{star("cA")}

	// 无权限的查询者：不可见链接被当作不存在，路径改道。
	res := query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: pat, Principal: "alice"})
	if !res.Found || res.Cost != 2 {
		t.Fatalf("expected rerouted cost 2 path, got %+v", res)
	}
	if want := []ontology.ObjectID{"s", "x", "t"}; !reflect.DeepEqual(res.Objects, want) {
		t.Fatalf("objects = %v, want %v", res.Objects, want)
	}

	// 有权限的查询者：走代价 1 的直达链接。
	res = query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: pat, Principal: "admin"})
	if !res.Found || res.Cost != 1 {
		t.Fatalf("expected cost 1 direct path, got %+v", res)
	}

	// 唯一的连接不可见时，结果为不可达而非错误。
	res = query(t, g, ontology.PathQuery{Start: "s", End: "y", Pattern: pat, Principal: "alice"})
	if res.Found {
		t.Fatalf("expected unreachable for alice, got %+v", res)
	}
	res = query(t, g, ontology.PathQuery{Start: "s", End: "y", Pattern: pat, Principal: "admin"})
	if !res.Found || res.Cost != 1 {
		t.Fatalf("expected cost 1 path for admin, got %+v", res)
	}
}

func TestDecisionPrecedence(t *testing.T) {
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "LA", From: "T1", To: "T1", Category: "cA", Cost: 1},
	)
	addObjects(t, g, "T1", "s", "t")
	addObjects(t, g, "TF", "fs", "ft")
	// s 与 t 之间没有任何链接：用于「禁止 > 不可达」的次序验证。

	middleStar := []ontology.PatternElem{cat("cA"), {Category: "cB", Star: true}, cat("cC")}
	cases := []struct {
		name string
		q    ontology.PathQuery
		want error // nil 表示不可达（合法结果）
	}{
		{"empty pattern", ontology.PathQuery{Start: "s", End: "t"}, ontology.ErrInvalidParams},
		{"middle star", ontology.PathQuery{Start: "s", End: "t", Pattern: middleStar}, ontology.ErrInvalidParams},
		{"unknown category", ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{cat("cX")}}, ontology.ErrInvalidParams},
		{"missing start", ontology.PathQuery{Start: "nope", End: "t", Pattern: []ontology.PatternElem{cat("cA")}}, ontology.ErrInvalidParams},
		{"missing end", ontology.PathQuery{Start: "s", End: "nope", Pattern: []ontology.PatternElem{cat("cA")}}, ontology.ErrInvalidParams},
		{"forbidden start type", ontology.PathQuery{Start: "fs", End: "t", Pattern: []ontology.PatternElem{cat("cA")}}, ontology.ErrForbiddenObjectType},
		{"forbidden end type", ontology.PathQuery{Start: "s", End: "ft", Pattern: []ontology.PatternElem{cat("cA")}}, ontology.ErrForbiddenObjectType},
		// 参数非法优先于类型禁止。
		{"invalid beats forbidden", ontology.PathQuery{Start: "fs", End: "ft"}, ontology.ErrInvalidParams},
		// 类型禁止优先于不可达。
		{"forbidden beats unreachable", ontology.PathQuery{Start: "fs", End: "t", Pattern: []ontology.PatternElem{cat("cA")}}, ontology.ErrForbiddenObjectType},
		// 不可达是合法结果，不是错误。
		{"unreachable is not an error", ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{cat("cA")}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := g.Query(tc.q)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if res.Found {
					t.Fatalf("expected unreachable, got %+v", res)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLinkDirection(t *testing.T) {
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "DIR", From: "T1", To: "T1", Category: "cA", Cost: 1},
		ontology.LinkTypeSpec{ID: "BI", From: "T1", To: "T1", Bidirectional: true, Category: "cB", Cost: 1},
	)
	addObjects(t, g, "T1", "s", "t", "u", "v")
	addLink(t, g, "l1", "DIR", "s", "t")
	addLink(t, g, "l2", "BI", "u", "v")

	res := query(t, g, ontology.PathQuery{Start: "s", End: "t", Pattern: []ontology.PatternElem{cat("cA")}})
	if !res.Found {
		t.Fatal("expected forward traversal to succeed")
	}
	// 单向链接不得反向遍历。
	res = query(t, g, ontology.PathQuery{Start: "t", End: "s", Pattern: []ontology.PatternElem{cat("cA")}})
	if res.Found {
		t.Fatalf("expected no reverse path on directional link, got %+v", res)
	}
	// 双向链接两个方向都可遍历。
	res = query(t, g, ontology.PathQuery{Start: "v", End: "u", Pattern: []ontology.PatternElem{cat("cB")}})
	if !res.Found {
		t.Fatal("expected reverse traversal on bidirectional link to succeed")
	}
}

func TestAddLinkEndpointTypeValidation(t *testing.T) {
	g := mustGraph(t)
	registerTypes(t, g,
		ontology.LinkTypeSpec{ID: "L12", From: "T1", To: "T2", Category: "cA", Cost: 1},
		ontology.LinkTypeSpec{ID: "B12", From: "T1", To: "T2", Bidirectional: true, Category: "cB", Cost: 1},
	)
	addObjects(t, g, "T1", "a1", "a2")
	addObjects(t, g, "T2", "b1")

	if err := g.AddLink("bad", "L12", "a1", "a2"); err == nil {
		t.Fatal("expected endpoint type mismatch error")
	}
	must(t, g.AddLink("ok", "L12", "a1", "b1"))
	// 双向链接允许端点类型互换。
	must(t, g.AddLink("ok-swapped", "B12", "b1", "a1"))
	if err := g.AddLink("bad-type", "NOPE", "a1", "b1"); err == nil {
		t.Fatal("expected unknown link type error")
	}
}

func TestLinkTypeCostRange(t *testing.T) {
	g := mustGraph(t)
	if err := g.RegisterLinkType(ontology.LinkTypeSpec{ID: "NEG", From: "T1", To: "T1", Category: "cA", Cost: -1}); err == nil {
		t.Fatal("expected negative cost rejection")
	}
	if err := g.RegisterLinkType(ontology.LinkTypeSpec{ID: "BIG", From: "T1", To: "T1", Category: "cA", Cost: ontology.MaxLinkCost + 1}); err == nil {
		t.Fatal("expected cost overflow rejection")
	}
	must(t, g.RegisterLinkType(ontology.LinkTypeSpec{ID: "ZERO", From: "T1", To: "T1", Category: "cA", Cost: 0}))
}
