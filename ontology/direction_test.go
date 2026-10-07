package ontology

import "testing"

// TestLinkDirections 验证正向、逆向、双向链接类型的遍历方向。
func TestLinkDirections(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for _, id := range []string{"s", "a", "b", "c"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	st.AddLinkType(LinkType{ID: "fwd", Direction: DirectionOut})
	st.AddLinkType(LinkType{ID: "rev", Direction: DirectionIn})
	st.AddLinkType(LinkType{ID: "und", Direction: DirectionBoth})

	st.AddLink(Link{Type: "fwd", From: "s", To: "a"}) // s 正向可见 a
	st.AddLink(Link{Type: "rev", From: "b", To: "s"}) // 仅逆向，s 可回到 b
	st.AddLink(Link{Type: "und", From: "c", To: "s"}) // 双向，s 可见 c

	tr := NewTraverser(st)
	got, reason := drain(t, tr, nil, TraverseParams{Start: "s", MaxDepth: 1, MaxFanout: 10, PageSize: 10})
	if reason != TruncationNone {
		t.Fatalf("reason=%v", reason)
	}
	if want := ids("s", "a", "b", "c"); !equalIDs(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	// 从 a 出发：fwd 不可逆，看不到 s。
	got, _ = drain(t, tr, nil, TraverseParams{Start: "a", MaxDepth: 2, MaxFanout: 10, PageSize: 10})
	if want := ids("a"); !equalIDs(got, want) {
		t.Fatalf("from a: got %v want %v", got, want)
	}

	// 从 b 出发：rev 正向不可走，看不到 s。
	got, _ = drain(t, tr, nil, TraverseParams{Start: "b", MaxDepth: 2, MaxFanout: 10, PageSize: 10})
	if want := ids("b"); !equalIDs(got, want) {
		t.Fatalf("from b: got %v want %v", got, want)
	}

	// 从 c 出发：und 双向，可见 s 及其一跳后继。
	got, reason = drain(t, tr, nil, TraverseParams{Start: "c", MaxDepth: 2, MaxFanout: 10, PageSize: 10})
	if reason != TruncationNone {
		t.Fatalf("from c reason=%v want complete", reason)
	}
	if want := ids("c", "s", "a", "b"); !equalIDs(got, want) {
		t.Fatalf("from c: got %v want %v", got, want)
	}
}

// TestCyclesDedup：环与平行链接不会导致重复对象或无限遍历。
func TestCyclesDedup(t *testing.T) {
	st := NewStore(AllowAllPolicy{})
	for _, id := range []string{"s", "a", "b"} {
		st.AddObject(Object{ID: ObjectID(id)})
	}
	st.AddLinkType(LinkType{ID: "und", Direction: DirectionBoth})
	st.AddLink(Link{Type: "und", From: "s", To: "a"})
	st.AddLink(Link{Type: "und", From: "a", To: "b"})
	st.AddLink(Link{Type: "und", From: "b", To: "s"})
	// 平行重复链接。
	st.AddLink(Link{Type: "und", From: "s", To: "a"})

	tr := NewTraverser(st)
	got, reason := drain(t, tr, nil, TraverseParams{Start: "s", MaxDepth: 5, MaxFanout: 10, PageSize: 1})
	if reason != TruncationNone {
		t.Fatalf("reason=%v", reason)
	}
	if want := ids("s", "a", "b"); !equalIDs(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
