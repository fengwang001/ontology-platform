package ontology_test

import (
	"fmt"
	"sort"
	"testing"

	ont "ontology/ontology"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func linkType(g *ont.Graph, id string) *ont.LinkType {
	switch id {
	case "D":
		return &ont.LinkType{ID: "D", Direction: ont.Directed, AllowSelfLoop: true, AllowMultiple: true}
	case "B":
		return &ont.LinkType{ID: "B", Direction: ont.Bidirectional, AllowSelfLoop: true, AllowMultiple: true}
	default:
		return &ont.LinkType{ID: "DS", Direction: ont.Directed, AllowSelfLoop: false, AllowMultiple: false}
	}
}

func newTestGraph(t *testing.T) *ont.Graph {
	t.Helper()
	g := ont.NewGraph()
	must(t, g.AddObjectType(&ont.ObjectType{ID: "T"}))
	must(t, g.AddLinkType(linkType(g, "D")))
	must(t, g.AddLinkType(linkType(g, "B")))
	must(t, g.AddLinkType(linkType(g, "DS")))
	must(t, g.RegisterCaller("alice"))
	must(t, g.RegisterCaller("bob"))
	return g
}

func grantAll(t *testing.T, g *ont.Graph, caller string, objs, links []string) {
	t.Helper()
	for _, o := range objs {
		must(t, g.GrantExist(caller, o))
	}
	for _, l := range links {
		must(t, g.GrantTraverse(caller, l))
	}
}

func verifyEvidence(t *testing.T, res ont.CycleResult) {
	t.Helper()
	if !res.HasCycle {
		return
	}
	if len(res.Cycle) == 0 {
		t.Fatalf("has cycle but empty evidence")
	}
	seen := map[string]bool{}
	for _, id := range res.Cycle {
		if seen[id] {
			t.Fatalf("evidence %v is not a simple cycle (duplicate %s)", res.Cycle, id)
		}
		seen[id] = true
	}
}

// TestSelfLoop 长度为一的自环；去掉遍历权限后环必须消失。
func TestSelfLoop(t *testing.T) {
	g := newTestGraph(t)
	must(t, g.AddObject("a", "T"))
	must(t, g.AddLink(&ont.Link{ID: "l1", Type: linkType(g, "D"), Source: "a", Target: "a"}))
	grantAll(t, g, "alice", []string{"a"}, []string{"l1"})

	res, err := g.HasCycle("alice")
	must(t, err)
	if !res.HasCycle || len(res.Cycle) != 1 || res.Cycle[0] != "a" {
		t.Fatalf("self loop not detected: %+v", res)
	}

	must(t, g.RevokeTraverse("alice", "l1"))
	res, err = g.HasCycle("alice")
	must(t, err)
	if res.HasCycle {
		t.Fatalf("self loop should be excluded without traverse perm: %+v", res)
	}
}

// TestBidirectionalRoundTrip 双向链接的往返构成长度为二的环；单向不构成。
func TestBidirectionalRoundTrip(t *testing.T) {
	g := newTestGraph(t)
	must(t, g.AddObject("a", "T"))
	must(t, g.AddObject("b", "T"))
	must(t, g.AddLink(&ont.Link{ID: "l1", Type: linkType(g, "D"), Source: "a", Target: "b"}))
	grantAll(t, g, "alice", []string{"a", "b"}, []string{"l1"})

	res, err := g.HasCycle("alice")
	must(t, err)
	if res.HasCycle {
		t.Fatalf("single directed arc must not be a 2-cycle: %+v", res)
	}

	must(t, g.DeleteLink("l1"))
	must(t, g.AddLink(&ont.Link{ID: "l2", Type: linkType(g, "B"), Source: "a", Target: "b"}))
	must(t, g.GrantTraverse("alice", "l2"))
	res, err = g.HasCycle("alice")
	must(t, err)
	if !res.HasCycle || len(res.Cycle) != 2 {
		t.Fatalf("bidirectional round trip must be a cycle: %+v", res)
	}
	sort.Strings(res.Cycle)
	if res.Cycle[0] != "a" || res.Cycle[1] != "b" {
		t.Fatalf("wrong 2-cycle evidence: %v", res.Cycle)
	}
}

// TestOnlyCycleLinkExcluded 唯一构成环的链接被遍历权限排除；恢复后无环变有环。
func TestOnlyCycleLinkExcluded(t *testing.T) {
	g := newTestGraph(t)
	must(t, g.AddObject("a", "T"))
	must(t, g.AddObject("b", "T"))
	must(t, g.AddObject("c", "T"))
	must(t, g.AddLink(&ont.Link{ID: "ab", Type: linkType(g, "D"), Source: "a", Target: "b"}))
	must(t, g.AddLink(&ont.Link{ID: "bc", Type: linkType(g, "D"), Source: "b", Target: "c"}))
	must(t, g.AddLink(&ont.Link{ID: "ca", Type: linkType(g, "D"), Source: "c", Target: "a"}))
	grantAll(t, g, "alice", []string{"a", "b", "c"}, []string{"ab", "bc"})

	res, err := g.HasCycle("alice")
	must(t, err)
	if res.HasCycle {
		t.Fatalf("cycle must not exist when closing link lacks traverse perm")
	}

	must(t, g.GrantTraverse("alice", "ca"))
	res, err = g.HasCycle("alice")
	must(t, err)
	if !res.HasCycle || len(res.Cycle) != 3 {
		t.Fatalf("cycle must appear after restoring traverse perm: %+v", res)
	}
	verifyEvidence(t, res)
}

// TestExistFilterOrder 存在性过滤先于遍历过滤。
func TestExistFilterOrder(t *testing.T) {
	g := newTestGraph(t)
	must(t, g.AddObject("a", "T"))
	must(t, g.AddObject("b", "T"))
	must(t, g.AddLink(&ont.Link{ID: "ab", Type: linkType(g, "B"), Source: "a", Target: "b"}))
	must(t, g.GrantExist("alice", "a"))
	must(t, g.GrantTraverse("alice", "ab"))

	res, err := g.HasCycle("alice")
	must(t, err)
	if res.HasCycle {
		t.Fatalf("edge to invisible object must be removed before cycle test")
	}
}

// TestEmptyVisible 可见对象为空：直接无环，不是错误。
func TestEmptyVisible(t *testing.T) {
	g := newTestGraph(t)
	must(t, g.AddObject("a", "T"))
	res, err := g.HasCycle("alice")
	must(t, err)
	if res.HasCycle || res.VisitedObjects != 0 {
		t.Fatalf("empty visible set => no cycle, no error, got %+v", res)
	}
}

// TestInvalidCaller 错误判定优先于一切。
func TestInvalidCaller(t *testing.T) {
	g := newTestGraph(t)
	if _, err := g.HasCycle(""); err != ont.ErrInvalidCaller {
		t.Fatalf("empty caller: want ErrInvalidCaller, got %v", err)
	}
	if _, err := g.HasCycle("ghost"); err != ont.ErrInvalidCaller {
		t.Fatalf("unknown caller: want ErrInvalidCaller, got %v", err)
	}
}

// TestParallelLinksNotDuplicated 多重同类型链接不得重复计入环证据。
func TestParallelLinksNotDuplicated(t *testing.T) {
	g := newTestGraph(t)
	must(t, g.AddObject("a", "T"))
	must(t, g.AddObject("b", "T"))
	for i := 0; i < 5; i++ {
		must(t, g.AddLink(&ont.Link{
			ID:     fmt.Sprintf("p%d", i),
			Type:   linkType(g, "B"),
			Source: "a", Target: "b",
		}))
	}
	grantAll(t, g, "alice", []string{"a", "b"},
		[]string{"p0", "p1", "p2", "p3", "p4"})

	res, err := g.HasCycle("alice")
	must(t, err)
	if !res.HasCycle || len(res.Cycle) != 2 {
		t.Fatalf("parallel links must collapse to one 2-cycle: %+v", res)
	}
}

// TestLinkTypeConstraints 自环禁止与多重链接禁止在写入时被拒绝。
func TestLinkTypeConstraints(t *testing.T) {
	g := newTestGraph(t)
	must(t, g.AddObject("a", "T"))
	must(t, g.AddObject("b", "T"))
	l := &ont.Link{ID: "s", Type: linkType(g, "DS"), Source: "a", Target: "a"}
	if err := g.AddLink(l); err != ont.ErrSelfLoop {
		t.Fatalf("want ErrSelfLoop, got %v", err)
	}
	must(t, g.AddLink(&ont.Link{ID: "ab1", Type: linkType(g, "DS"), Source: "a", Target: "b"}))
	if err := g.AddLink(&ont.Link{ID: "ab2", Type: linkType(g, "DS"), Source: "b", Target: "a"}); err != ont.ErrDuplicate {
		t.Fatalf("same unordered pair with AllowMultiple=false: want ErrDuplicate, got %v", err)
	}
}
