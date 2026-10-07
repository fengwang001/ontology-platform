package traversal

import "testing"

func TestBatchAtomicityAndValidation(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A")
	mustAddLinkTypes(t, g, tRel)

	// 合法批次：同批新增对象、链接类型与链接，一次发布。
	version, err := g.Batch(Mutation{
		AddLinkTypes: []LinkTypeID{"t2"},
		AddObjects:   []ObjectID{"B"},
		AddLinks:     []Link{{ID: "ab", Type: "t2", Source: "A", Target: "B"}},
	})
	if err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	if g.SnapshotVersion() != version {
		t.Fatalf("batch must publish exactly one new version")
	}

	before := g.SnapshotVersion()
	_, err = g.Batch(Mutation{
		AddObjects: []ObjectID{"C", "A"}, // A 已存在：整批必须失败
		AddLinks:   []Link{{ID: "cx", Type: tRel, Source: "C", Target: "A"}},
	})
	if err != ErrDuplicateObject {
		t.Fatalf("want ErrDuplicateObject, got %v", err)
	}
	if g.SnapshotVersion() != before {
		t.Fatalf("failed batch must not publish any version")
	}
	if err := g.AddObject(Object{ID: "C"}); err != nil {
		t.Fatalf("object C from rolled-back batch must not exist: %v", err)
	}

	// 端点缺失的链接整批拒绝。
	_, err = g.Batch(Mutation{
		AddLinks: []Link{{ID: "bad", Type: tRel, Source: "A", Target: "ZZ"}},
	})
	if err != ErrObjectMissing {
		t.Fatalf("want ErrObjectMissing, got %v", err)
	}

	// 删除对象连带删除链接。
	if err := g.AddLink(Link{ID: "self", Type: tRel, Source: "A", Target: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Batch(Mutation{}); err != nil {
		t.Fatalf("empty batch must be a no-op success: %v", err)
	}
	g.DeleteObject("A")
	res, err := TraverseSnapshot(g.loadSnapshot(), TraversalRequest{
		Start: "B", Directions: map[LinkTypeID]Direction{"t2": DirOutbound}, MaxDepth: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A 被删后 t2 链接已级联删除，B 成为边界。
	if statusCounts(res)[StatusBoundary] != 1 {
		t.Fatalf("cascading link delete failed: %#v", shapes(res))
	}
}

func TestSingleOpValidation(t *testing.T) {
	g := NewGraph()
	if err := g.AddLink(Link{ID: "x", Type: "no", Source: "A", Target: "B"}); err != ErrLinkTypeMissing {
		t.Fatalf("want ErrLinkTypeMissing, got %v", err)
	}
	mustAddLinkTypes(t, g, tRel)
	if err := g.AddLink(Link{ID: "x", Type: tRel, Source: "A", Target: "B"}); err != ErrObjectMissing {
		t.Fatalf("want ErrObjectMissing, got %v", err)
	}
	if err := g.AddLinkType(tRel); err != ErrDuplicateLinkType {
		t.Fatalf("want ErrDuplicateLinkType, got %v", err)
	}
}
