package ontology

import "testing"

// 翻页期间：靠前插入不会出现、靠后未翻到的元素被删除后不再出现；
// 变更标记要能区分插入与删除；丢弃与截断必须分别可观察。
func TestMutationVisibilityAndMarkers(t *testing.T) {
	st := seededStore(8) // key-00 .. key-07

	p1, _ := st.Scan("", 2)
	if got := keysOf(p1); got[0] != keyOf(0) || got[1] != keyOf(1) {
		t.Fatalf("page1 = %v", got)
	}

	// 插入一个排序位置在当前游标之前的元素；删除一个尚未翻到的靠后元素。
	st.Put("aaa-inserted-before", 100)
	if !st.Delete(keyOf(5)) {
		t.Fatal("seed key must exist")
	}

	p2, _ := st.Scan(p1.NextCursor, 2)
	// 插入元素不出现，后续页仍是快照中原有且未删除的元素。
	for _, item := range p2.Items {
		if item.Key == "aaa-inserted-before" {
			t.Fatal("element inserted before cursor must not appear")
		}
	}
	if got := keysOf(p2); got[0] != keyOf(2) || got[1] != keyOf(3) {
		t.Fatalf("page2 = %v", got)
	}
	// 本页未跨到被删元素，因此没有丢弃；但因 limit 截断，hasMore 为 true。
	if p2.Dropped != 0 || !p2.HasMore {
		t.Fatalf("page2 dropped=%d hasMore=%v", p2.Dropped, p2.HasMore)
	}
	if !p2.Changes.Inserted || !p2.Changes.Deleted {
		t.Fatalf("both change markers must be set: %+v", p2.Changes)
	}
	if p2.Changes.InsertedCount != 1 || p2.Changes.DeletedCount != 1 {
		t.Fatalf("change counts wrong: %+v", p2.Changes)
	}

	// 第三页取 4 个：快照剩余 key-04,key-05(del),key-06,key-07，
	// 拿到 3 个，key-05 属于“丢弃”（dropped=1），且到快照末尾不算截断。
	p3, _ := st.Scan(p2.NextCursor, 4)
	if got := keysOf(p3); len(got) != 3 || got[1] != keyOf(6) {
		t.Fatalf("page3 = %v", got)
	}
	if p3.Dropped != 1 {
		t.Fatalf("deleted element must be dropped, got dropped=%d", p3.Dropped)
	}
	if p3.HasMore {
		t.Fatal("reached snapshot end: deletion is not truncation, hasMore must be false")
	}

	stats, changes, err := st.Stats(p3.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Deleted != 1 || stats.Inserted != 1 || stats.Truncated != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if !changes.Inserted || !changes.Deleted {
		t.Fatalf("changes = %+v", changes)
	}

	// 截断单独可观察：对会话开头再开一次 limit=2，Truncated 应为剩余 6。
	other, _ := st.Scan("", 2)
	stats2, _, err := st.Stats(other.NextCursor)
	if err != nil || stats2.Truncated != 6 || stats2.Deleted != 0 {
		t.Fatalf("truncation stats = %+v err=%v", stats2, err)
	}
}

// 只插入时删除标记不得置位，反之亦然。
func TestChangeMarkersAreIndependent(t *testing.T) {
	st := seededStore(4)
	p, _ := st.Scan("", 2)
	st.Put("zzz-new", 1)
	p2, _ := st.Scan(p.NextCursor, 2)
	if !p2.Changes.Inserted || p2.Changes.Deleted {
		t.Fatalf("insert-only markers wrong: %+v", p2.Changes)
	}

	st2 := seededStore(4)
	q, _ := st2.Scan("", 2)
	st2.Delete(keyOf(3))
	q2, _ := st2.Scan(q.NextCursor, 2)
	if q2.Changes.Inserted || !q2.Changes.Deleted {
		t.Fatalf("delete-only markers wrong: %+v", q2.Changes)
	}
}

// 删除当前页之后的全部剩余元素：下一页为空、有丢弃、hasMore=false。
func TestDeleteAllRemaining(t *testing.T) {
	st := seededStore(4)
	p, _ := st.Scan("", 2)
	st.Delete(keyOf(2))
	st.Delete(keyOf(3))
	p2, err := st.Scan(p.NextCursor, 2)
	if err != nil || len(p2.Items) != 0 || p2.HasMore || p2.Dropped != 2 {
		t.Fatalf("page = %+v err=%v", p2, err)
	}
}
