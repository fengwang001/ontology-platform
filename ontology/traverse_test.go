package ontology

import (
	"reflect"
	"testing"
)

// must 在测试内以闭包形式使用：must := mkMust(t); v := must(s.Op(...))
func mkMust(t *testing.T) func(Version, error) Version {
	t.Helper()
	return func(v Version, err error) Version {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return v
	}
}

func propsOf(t *testing.T, res *TraverseResult, id string) map[string]Value {
	t.Helper()
	for _, n := range res.Nodes {
		if n.ID == id {
			return n.Props
		}
	}
	t.Fatalf("node %q not in result", id)
	return nil
}

func normOf(res *TraverseResult) ([]string, []string) { return normalize(res) }

// 属性定义迁移前后边界：遍历锚定 AsOf 时刻的定义版本，
// 且迁移是否已完成不影响历史时刻的输出。
func TestTraverseSchemaMigrationBoundary(t *testing.T) {
	must := mkMust(t)
	s := NewStore()
	v1 := must(s.DefineObjectType("doc", []PropertyDef{
		{ID: "p1", Name: "title", Type: TypeString},
	}))
	v2 := must(s.PutObject("doc", "a", map[string]Value{"title": "hello"}))
	v3 := must(s.SetProperty("a", "title", "world"))

	req := func(asOf Version) TraverseRequest {
		return TraverseRequest{Start: "a", AsOf: asOf, MaxDepth: 4, MaxVisited: 16}
	}
	beforeV2, err := s.Traverse(req(v2))
	if err != nil {
		t.Fatal(err)
	}
	beforeV3, err := s.Traverse(req(v3))
	if err != nil {
		t.Fatal(err)
	}

	// 迁移：title 更名为 headline，并新增 views。
	v4 := must(s.MigrateObjectType("doc",
		[]PropertyDef{
			{ID: "p1", Name: "headline", Type: TypeString},
			{ID: "p2", Name: "views", Type: TypeInt},
		}, nil))
	v5 := must(s.SetProperty("a", "headline", "new"))
	v6 := must(s.SetProperty("a", "views", int64(7)))

	cases := []struct {
		asOf Version
		want map[string]Value
	}{
		{v2, map[string]Value{"title": "hello"}},
		{v3, map[string]Value{"title": "world"}},
		{v4, map[string]Value{"headline": "world"}},
		{v5, map[string]Value{"headline": "new"}},
		{v6, map[string]Value{"headline": "new", "views": int64(7)}},
	}
	for _, c := range cases {
		res, err := s.Traverse(req(c.asOf))
		if err != nil {
			t.Fatalf("asOf=%d: %v", c.asOf, err)
		}
		if got := propsOf(t, res, "a"); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("asOf=%d: got %v, want %v", c.asOf, got, c.want)
		}
	}

	// 迁移完成后，历史时刻的输出必须与迁移前完全一致。
	afterV2, _ := s.Traverse(req(v2))
	afterV3, _ := s.Traverse(req(v3))
	if n1, e1 := normOf(beforeV2); true {
		n2, e2 := normOf(afterV2)
		if !reflect.DeepEqual(n1, n2) || !reflect.DeepEqual(e1, e2) {
			t.Fatal("result at v2 changed after migration")
		}
	}
	if n1, e1 := normOf(beforeV3); true {
		n2, e2 := normOf(afterV3)
		if !reflect.DeepEqual(n1, n2) || !reflect.DeepEqual(e1, e2) {
			t.Fatal("result at v3 changed after migration")
		}
	}

	// 判定日志必须记录锚定的定义版本：v3 时刻覆盖的仍是初始定义版本。
	res, _ := s.Traverse(req(v3))
	found := false
	for _, d := range res.Decisions {
		if d.Kind == "schema-select" && d.BasisFrom == v1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no schema-select decision anchored at v%d: %+v", v1, res.Decisions)
	}
}
