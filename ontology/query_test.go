package ontology

import (
	"errors"
	"fmt"
	"testing"
)

// chainEnv 构造用于路径查询测试的图与注册表。
type chainEnv struct {
	reg     *PermissionRegistry
	graph   *Graph
	service *Service
}

func newChainEnv(t *testing.T) *chainEnv {
	t.Helper()
	reg := NewPermissionRegistry(DenyOverrides)
	return &chainEnv{reg: reg, graph: NewGraph()}
}

func (e *chainEnv) build(t *testing.T) {
	t.Helper()
	e.service = NewService(e.reg, e.graph)
}

// allowAll 让主体 p 对所有链接类型放行。
func (e *chainEnv) allowAll(p PrincipalID) {
	e.reg.UpsertGroup("g", 1)
	e.reg.AssignPrincipal(p, "g")
	e.reg.SetLinkTypeDeclaration("g", "L", DeclAllow)
}

func mustLink(t *testing.T, g *Graph, from, to ObjectID, lt LinkTypeID, cost int64) {
	t.Helper()
	if err := g.AddLink(Link{From: from, To: to, Type: lt, Cost: cost}); err != nil {
		t.Fatalf("AddLink %s->%s: %v", from, to, err)
	}
}

func TestShortestPathMinCost(t *testing.T) {
	env := newChainEnv(t)
	for _, id := range []ObjectID{"S", "A", "B", "T"} {
		env.graph.AddObject(id, "T"+ObjectTypeID(id))
	}
	mustLink(t, env.graph, "S", "A", "L", 1)
	mustLink(t, env.graph, "A", "T", "L", 1)
	mustLink(t, env.graph, "S", "B", "L", 5)
	mustLink(t, env.graph, "B", "T", "L", 5)
	env.allowAll("p")
	env.build(t)

	res, err := env.service.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusReachable {
		t.Fatalf("status = %v", res.Status)
	}
	if res.Cost != 2 {
		t.Fatalf("cost = %d, want 2", res.Cost)
	}
	want := []ObjectID{"S", "A", "T"}
	if fmt.Sprint(res.Path) != fmt.Sprint(want) {
		t.Fatalf("path = %v, want %v", res.Path, want)
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	env := newChainEnv(t)
	for _, id := range []ObjectID{"S", "A", "B", "T"} {
		env.graph.AddObject(id, "T"+ObjectTypeID(id))
	}
	// 两条等代价路径：S-A-T 与 S-B-T，应选对象标识序列字典序更小的 S-A-T。
	mustLink(t, env.graph, "S", "B", "L", 1)
	mustLink(t, env.graph, "B", "T", "L", 1)
	mustLink(t, env.graph, "S", "A", "L", 1)
	mustLink(t, env.graph, "A", "T", "L", 1)
	env.allowAll("p")
	env.build(t)

	res, err := env.service.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	want := []ObjectID{"S", "A", "T"}
	if fmt.Sprint(res.Path) != fmt.Sprint(want) {
		t.Fatalf("path = %v, want %v", res.Path, want)
	}
}

func TestLexicographicTieBreakLongerSequence(t *testing.T) {
	env := newChainEnv(t)
	for _, id := range []ObjectID{"S", "A", "A1", "B", "T"} {
		env.graph.AddObject(id, "T"+ObjectTypeID(id))
	}
	// 等代价下三跳路径 [S,A,A1,T] 字典序小于两跳路径 [S,B,T]。
	mustLink(t, env.graph, "S", "A", "L", 1)
	mustLink(t, env.graph, "A", "A1", "L", 1)
	mustLink(t, env.graph, "A1", "T", "L", 1)
	mustLink(t, env.graph, "S", "B", "L", 3)
	mustLink(t, env.graph, "B", "T", "L", 3)
	env.allowAll("p")
	env.build(t)

	res, err := env.service.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	want := []ObjectID{"S", "A", "A1", "T"}
	if fmt.Sprint(res.Path) != fmt.Sprint(want) {
		t.Fatalf("path = %v, want %v", res.Path, want)
	}
	if res.Cost != 3 {
		t.Fatalf("cost = %d, want 3", res.Cost)
	}
}

func TestPermissionRedirectsToAllowedPath(t *testing.T) {
	env := newChainEnv(t)
	for _, id := range []ObjectID{"S", "A", "B", "T"} {
		env.graph.AddObject(id, "T"+ObjectTypeID(id))
	}
	mustLink(t, env.graph, "S", "A", "L", 1)
	mustLink(t, env.graph, "A", "T", "L", 1)
	mustLink(t, env.graph, "S", "B", "L", 5)
	mustLink(t, env.graph, "B", "T", "L", 5)
	// 链接类型层不声明，通过对象类型层放行；拒绝 A 所在对象类型，
	// 迫使查询选择更贵的 S-B-T。
	env.reg.UpsertGroup("g", 1)
	env.reg.AssignPrincipal("p", "g")
	env.reg.SetObjectTypeDeclaration("g", "TS", DeclAllow)
	env.reg.SetObjectTypeDeclaration("g", "TB", DeclAllow)
	env.reg.SetObjectTypeDeclaration("g", "TT", DeclAllow)
	env.reg.SetObjectTypeDeclaration("g", "TA", DeclDeny)
	env.build(t)

	res, err := env.service.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	want := []ObjectID{"S", "B", "T"}
	if fmt.Sprint(res.Path) != fmt.Sprint(want) {
		t.Fatalf("path = %v, want %v", res.Path, want)
	}
	if res.Cost != 10 {
		t.Fatalf("cost = %d, want 10", res.Cost)
	}
}

func TestUnreachableWhenAllPathsDenied(t *testing.T) {
	env := newChainEnv(t)
	for _, id := range []ObjectID{"S", "A", "T"} {
		env.graph.AddObject(id, "T"+ObjectTypeID(id))
	}
	mustLink(t, env.graph, "S", "A", "L", 1)
	mustLink(t, env.graph, "A", "T", "L", 1)
	env.allowAll("p")
	env.reg.SetLinkTypeDeclaration("g", "L", DeclDeny)
	env.build(t)

	res, err := env.service.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnreachable {
		t.Fatalf("status = %v, want unreachable", res.Status)
	}
	if res.Path != nil {
		t.Fatalf("path = %v, want nil", res.Path)
	}
}

func TestSameSourceAndTarget(t *testing.T) {
	env := newChainEnv(t)
	env.graph.AddObject("S", "TS")
	env.build(t)

	res, err := env.service.ShortestPath("p", "S", "S")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusReachable || res.Cost != 0 || len(res.Path) != 1 {
		t.Fatalf("res = %+v, want trivial reachable", res)
	}
}

func TestInvalidCostRejected(t *testing.T) {
	g := NewGraph()
	g.AddObject("A", "TA")
	g.AddObject("B", "TB")
	if err := g.AddLink(Link{From: "A", To: "B", Type: "L", Cost: 0}); !errors.Is(err, ErrInvalidCost) {
		t.Fatalf("err = %v, want ErrInvalidCost", err)
	}
	if err := g.AddLink(Link{From: "A", To: "B", Type: "L", Cost: -3}); !errors.Is(err, ErrInvalidCost) {
		t.Fatalf("err = %v, want ErrInvalidCost", err)
	}
}
