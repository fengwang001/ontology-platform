package ontology

import (
	"math/rand"
	"sort"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	must(t, g.AddObjectType(ObjectType{Name: "Thing"}))
	must(t, g.AddLinkType(LinkType{Name: "dir", Direction: Directed, AllowSelf: true,
		AllowMulti: true, SourceType: "Thing", TargetType: "Thing"}))
	must(t, g.AddLinkType(LinkType{Name: "bi", Direction: Bidirectional, AllowSelf: true,
		AllowMulti: true, SourceType: "Thing", TargetType: "Thing"}))
	return g
}

func addObjs(t *testing.T, g *Graph, ids ...string) {
	t.Helper()
	for _, id := range ids {
		must(t, g.CreateObject(Object{ID: id, Type: "Thing"}))
	}
}

func addLink(t *testing.T, g *Graph, id, typ, src, dst string) {
	t.Helper()
	must(t, g.CreateLink(Link{ID: id, Type: typ, Source: src, Target: dst}))
}

// grant 给 caller 授予 objs 中对象的存在性权限与 linkIDs 的遍历权限。
func grant(g *Graph, caller string, objs []string, linkIDs ...string) {
	oe := map[string]bool{}
	for _, id := range objs {
		oe[id] = true
	}
	lt := map[string]bool{}
	for _, id := range linkIDs {
		lt[id] = true
	}
	g.SetPermissions(caller, Permissions{ExistObject: oe, TraverseLink: lt})
}

func grantAll(g *Graph, caller string, linkIDs ...string) {
	var objs []string
	for id := range g.objects {
		objs = append(objs, id)
	}
	sort.Strings(objs)
	grant(g, caller, objs, linkIDs...)
}

func sameSet(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// buildSnapshotForTest 在一致快照上供测试直接取内部子图与度量。
func buildSnapshotForTest(t *testing.T, g *Graph, caller string) (*visibleSubgraph, Result, Metrics) {
	t.Helper()
	g.mu.RLock()
	defer g.mu.RUnlock()
	s, r, m, err := g.buildSnapshot(caller)
	if err != nil {
		t.Fatalf("buildSnapshot: %v", err)
	}
	return s, r, m
}

// shuffledCopy 返回同一快照但根顺序、邻接顺序被确定性打乱的副本，
// 用于证明环检测结果不依赖遍历起始对象与访问顺序。
func (s *visibleSubgraph) shuffledCopy(seed int64) *visibleSubgraph {
	r := rand.New(rand.NewSource(seed))
	cp := &visibleSubgraph{
		objects:   append([]string(nil), s.objects...),
		adjacency: make(map[string][]arc, len(s.adjacency)),
		selfLoops: make(map[string][]string, len(s.selfLoops)),
	}
	r.Shuffle(len(cp.objects), func(i, j int) { cp.objects[i], cp.objects[j] = cp.objects[j], cp.objects[i] })
	for v, arcs := range s.adjacency {
		na := append([]arc(nil), arcs...)
		r.Shuffle(len(na), func(i, j int) { na[i], na[j] = na[j], na[i] })
		cp.adjacency[v] = na
	}
	for v, ls := range s.selfLoops {
		nl := append([]string(nil), ls...)
		r.Shuffle(len(nl), func(i, j int) { nl[i], nl[j] = nl[j], nl[i] })
		cp.selfLoops[v] = nl
	}
	return cp
}
