package ontology

import "testing"

// TestMutationDuringScan 快照语义 + 插入/删除变更标记 + 不重不漏。
func TestMutationDuringScan(t *testing.T) {
	s := seedStore(10) // k000..k009

	p1, err := s.Scan("", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflectKeys(keys(p1), []string{"k000", "k001", "k002"}) {
		t.Fatalf("page1=%v", keys(p1))
	}

	// 插入一个排序位置在续点之前的新元素；删除一个尚未翻到的元素。
	s.Put("k002a", 99) // 排在 k002 之后、k003 之前（游标之前）
	s.Delete("k007")

	p2, err := s.Scan(p1.Cursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflectKeys(keys(p2), []string{"k003", "k004", "k005"}) {
		t.Fatalf("page2=%v", keys(p2))
	}
	if p2.Changes != ChangeBoth {
		t.Fatalf("Changes=%v, want insert+delete", p2.Changes)
	}
	if !p2.HasMore || !p2.Truncated || p2.Discarded != 0 {
		t.Fatalf("page2 truncated/clean: %+v", p2)
	}

	p3, err := s.Scan(p2.Cursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	// k006、k008、k009 返回；k007 被丢弃；插入的 k002a 不出现。
	if !reflectKeys(keys(p3), []string{"k006", "k008", "k009"}) {
		t.Fatalf("page3=%v", keys(p3))
	}
	if p3.HasMore || p3.Truncated {
		t.Fatalf("final page must not be truncated: %+v", p3)
	}
	if p3.Discarded != 1 {
		t.Fatalf("page3 Discarded=%d want 1", p3.Discarded)
	}

	sid := p1.SessionID()
	rep := s.Skipped(sid)
	if !rep.Valid || rep.Total != 1 || rep.Reasons.DiscardedByDelete != 1 {
		t.Fatalf("skipped report=%+v", rep)
	}
	st := s.Stats(sid)
	if st.Inserted != 1 || st.Deleted != 1 || st.Discarded != 1 || st.Returned != 9 {
		t.Fatalf("stats=%+v", st)
	}

	all := append(keys(p1), append(keys(p2), keys(p3)...)...)
	if len(all) != 9 {
		t.Fatalf("expected 9 returned (no dup), got %v", all)
	}
	for _, k := range all {
		if k == "k002a" || k == "k007" {
			t.Fatalf("unexpected key %q in traversal", k)
		}
	}
}

// TestTruncateAndDiscardOnSamePage 截断与丢弃必须可分别观察。
func TestTruncateAndDiscardOnSamePage(t *testing.T) {
	s := seedStore(6) // k000..k005
	p1, _ := s.Scan("", 3)
	s.Delete("k004") // 删一个本页会扫到的元素
	p2, err := s.Scan(p1.Cursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 取满 3 个存活元素（k003,k005 + 续扫仍有剩余？快照剩余只有 k003,k004,k005）
	// k004 被丢弃，本页对象为 k003、k005，且已到快照末尾。
	if !reflectKeys(keys(p2), []string{"k003", "k005"}) {
		t.Fatalf("page2=%v", keys(p2))
	}
	if p2.Discarded != 1 || p2.Truncated {
		t.Fatalf("want discard=1, not truncated, got %+v", p2)
	}

	// 另一组：删元素但 limit 更小，截断与丢弃同时为真。
	s2 := seedStore(8)
	q1, _ := s2.Scan("", 2) // k000,k001
	s2.Delete("k003")
	q2, _ := s2.Scan(q1.Cursor, 3)
	if !reflectKeys(keys(q2), []string{"k002", "k004", "k005"}) {
		t.Fatalf("q2=%v", keys(q2))
	}
	if !q2.Truncated || q2.Discarded != 1 {
		t.Fatalf("want truncated=true and discarded=1, got %+v", q2)
	}
	if s2.Skipped(q1.SessionID()).Reasons.DiscardedByDelete != 1 {
		t.Fatal("cumulative skip reason mismatch")
	}
}

// TestDeleteAlreadyScanned 已翻过页的删除不影响后续，只打标记。
func TestDeleteAlreadyScanned(t *testing.T) {
	s := seedStore(4)
	p1, _ := s.Scan("", 2)
	s.Delete("k000")
	p2, err := s.Scan(p1.Cursor, 2)
	if err != nil || !reflectKeys(keys(p2), []string{"k002", "k003"}) {
		t.Fatalf("p2=%v err=%v", keys(p2), err)
	}
	if p2.Changes != ChangeDelete || s.Skipped(p1.SessionID()).Total != 0 {
		t.Fatalf("change=%v skip=%+v", p2.Changes, s.Skipped(p1.SessionID()))
	}
}

// TestInsertOnlyMarker 仅插入时标记可区分为 ChangeInsert。
func TestInsertOnlyMarker(t *testing.T) {
	s := seedStore(3)
	p1, _ := s.Scan("", 1)
	s.Put("zzz-new", 1)
	p2, err := s.Scan(p1.Cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Changes != ChangeInsert {
		t.Fatalf("Changes=%v", p2.Changes)
	}
	if s.Stats(p1.SessionID()).Deleted != 0 {
		t.Fatal("delete count must be 0")
	}
}

func reflectKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
