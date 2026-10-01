package merger

import (
	"reflect"
	"testing"
)

func errCode(err error) ErrCode {
	if err == nil {
		return 0
	}
	return err.(*Error).Code
}

func mustStats(t *testing.T, m *Merger, id int) Stats {
	t.Helper()
	st, err := m.Stats(id)
	if err != nil {
		t.Fatalf("stats(%d): %v", id, err)
	}
	return st
}

func mustPostings(t *testing.T, m *Merger, id int, term string) []Posting {
	t.Helper()
	list, err := m.Postings(id, term)
	if err != nil {
		t.Fatalf("postings(%d,%q): %v", id, term, err)
	}
	return list
}

// ids 次序不同导致新文档编号不同。
func TestMergeOrderRenumber(t *testing.T) {
	m := New()
	a, err := m.Register([]Doc{{Key: "a", Terms: []string{"x"}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Register([]Doc{{Key: "b", Terms: []string{"x"}}})
	if err != nil {
		t.Fatal(err)
	}

	// 先 a 后 b：a 的文档编号为 0。
	h1, err := m.BeginMerge([]int{a, b})
	if err != nil {
		t.Fatal(err)
	}
	ab, err := m.Commit(h1)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustPostings(t, m, ab, "x"); len(got) != 2 || got[0].Doc != 0 || got[1].Doc != 1 {
		t.Fatalf("order [a,b]: %+v", got)
	}

	// 再登记并按反序合并：d 的文档编号应为 0，c 的文档编号为 1。
	c, err := m.Register([]Doc{{Key: "c", Terms: []string{"y"}}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := m.Register([]Doc{{Key: "d", Terms: []string{"y"}}})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := m.BeginMerge([]int{d, c})
	if err != nil {
		t.Fatal(err)
	}
	dc, err := m.Commit(h2)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustPostings(t, m, dc, "y"); len(got) != 2 || got[0].Doc != 0 || got[1].Doc != 1 {
		t.Fatalf("order [d,c]: %+v", got)
	}
	if st := mustStats(t, m, dc); st.MaxDoc != 2 {
		t.Fatalf("maxDoc: %+v", st)
	}
}

// 合并后全部文档已删除的段得到 maxDoc 为 0 的新段。
func TestMergeAllDeletedGivesEmptySegment(t *testing.T) {
	m := New()
	a, _ := m.Register([]Doc{{Key: "a", Terms: []string{"x"}}, {Key: "b", Terms: []string{"y"}}})
	b, _ := m.Register([]Doc{{Key: "c", Terms: []string{"z"}}})
	for _, key := range []string{"a", "b", "c"} {
		if err := m.Delete(key); err != nil {
			t.Fatal(err)
		}
	}
	h, err := m.BeginMerge([]int{a, b})
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	if st := mustStats(t, m, id); st != (Stats{MaxDoc: 0, NumDocs: 0, TermCount: 0}) {
		t.Fatalf("empty merge stats: %+v", st)
	}
	if list := mustPostings(t, m, id, "x"); len(list) != 0 {
		t.Fatalf("expected empty postings, got %+v", list)
	}
}

// BeginMerge 之后删除再 Commit：maxDoc 保留，numDocs/df/termCount 随之减少。
func TestDeleteBetweenBeginAndCommit(t *testing.T) {
	m := New()
	a, _ := m.Register([]Doc{
		{Key: "a", Terms: []string{"x", "x", "y"}},
		{Key: "b", Terms: []string{"x", "z"}},
	})
	b, _ := m.Register([]Doc{{Key: "c", Terms: []string{"y"}}})

	h, err := m.BeginMerge([]int{a, b})
	if err != nil {
		t.Fatal(err)
	}
	// 删除文档 a（新编号 0）：y 的 df 降为 1（仅 c），x 的 df 从 2 降为 1，z 不变；
	// termCount 仍为 3（存活文档 b、c 覆盖 x、y、z）。
	if err := m.Delete("a"); err != nil {
		t.Fatal(err)
	}
	id, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	st := mustStats(t, m, id)
	if st != (Stats{MaxDoc: 3, NumDocs: 2, TermCount: 3}) {
		t.Fatalf("stats after replay delete: %+v", st)
	}
	xList := mustPostings(t, m, id, "x")
	if len(xList) != 1 || xList[0].Doc != 1 || xList[0].TF != 1 {
		t.Fatalf("x postings: %+v", xList)
	}
	if list := mustPostings(t, m, id, "y"); len(list) != 1 || list[0].Doc != 2 {
		t.Fatalf("y postings: %+v", list)
	}
	if list := mustPostings(t, m, id, "zzz"); len(list) != 0 {
		t.Fatalf("missing term postings: %+v", list)
	}
	// 已删除的键 a 可再次登记。
	if _, err := m.Register([]Doc{{Key: "a", Terms: nil}}); err != nil {
		t.Fatalf("re-register deleted key: %v", err)
	}
}

// 删除后同键重新登记再 Commit 不使旧文档复活。
func TestDeleteReregisterDuringMerge(t *testing.T) {
	m := New()
	a, _ := m.Register([]Doc{{Key: "k", Terms: []string{"old"}}})
	b, _ := m.Register([]Doc{{Key: "q", Terms: []string{"keep"}}})

	h, err := m.BeginMerge([]int{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("k"); err != nil {
		t.Fatal(err)
	}
	// 合并期间以同键登记新文档，旧冻结文档不得复活。
	c, err := m.Register([]Doc{{Key: "k", Terms: []string{"new"}}})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	if st := mustStats(t, m, merged); st != (Stats{MaxDoc: 2, NumDocs: 1, TermCount: 1}) {
		t.Fatalf("merged stats: %+v", st)
	}
	if list := mustPostings(t, m, merged, "old"); len(list) != 0 {
		t.Fatalf("old term should be gone: %+v", list)
	}
	if list := mustPostings(t, m, merged, "keep"); len(list) != 1 {
		t.Fatalf("keep postings: %+v", list)
	}
	if list := mustPostings(t, m, c, "new"); len(list) != 1 {
		t.Fatalf("new postings: %+v", list)
	}
	// 删除 k 必须命中新文档，旧文档不会复活。
	if err := m.Delete("k"); err != nil {
		t.Fatalf("delete must hit the new document: %v", err)
	}
	if err := m.Delete("k"); errCode(err) != ErrKeyNotFound {
		t.Fatalf("second delete must find no live key: %v", err)
	}
}

// 键恰在 BeginMerge 与 Commit 之间被删除并重新登记。
func TestDeleteAndReregisterKeyBetween(t *testing.T) {
	m := New()
	a, _ := m.Register([]Doc{{Key: "k", Terms: []string{"t"}}})
	b, _ := m.Register([]Doc{{Key: "r", Terms: []string{"t"}}})
	h, err := m.BeginMerge([]int{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("k"); err != nil {
		t.Fatal(err)
	}
	c, err := m.Register([]Doc{{Key: "k", Terms: []string{"t", "t"}}})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	// 新段编号 0 的旧 k 已删除（保留编号），编号 1 为 r。
	if list := mustPostings(t, m, merged, "t"); len(list) != 1 || list[0].Doc != 1 {
		t.Fatalf("merged t postings: %+v", list)
	}
	if list := mustPostings(t, m, c, "t"); len(list) != 1 || list[0].TF != 2 {
		t.Fatalf("new k postings: %+v", list)
	}
	if st := mustStats(t, m, merged); st.NumDocs != 1 || st.MaxDoc != 2 {
		t.Fatalf("merged stats: %+v", st)
	}
}

// 同一段不能同时处于两个合并；Abort 后可再次合并。
func TestSegmentBusyAndAbort(t *testing.T) {
	m := New()
	a, _ := m.Register([]Doc{{Key: "a"}})
	b, _ := m.Register([]Doc{{Key: "b"}})
	c, _ := m.Register([]Doc{{Key: "c"}})

	h1, err := m.BeginMerge([]int{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.BeginMerge([]int{b, c}); errCode(err) != ErrSegmentBusy {
		t.Fatalf("expected segment busy, got %v", err)
	}
	if err := m.Abort(h1); err != nil {
		t.Fatal(err)
	}
	if err := m.Abort(h1); errCode(err) != ErrInvalidHandle {
		t.Fatalf("double abort: %v", err)
	}
	h2, err := m.BeginMerge([]int{a, c})
	if err != nil {
		t.Fatalf("merge after abort: %v", err)
	}
	id, err := m.Commit(h2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(h2); errCode(err) != ErrInvalidHandle {
		t.Fatalf("double commit: %v", err)
	}
	if _, err := m.BeginMerge([]int{id, b}); err != nil {
		t.Fatalf("merged segment should be mergeable: %v", err)
	}
}

// 被拒绝的操作不消耗段编号与句柄号。
func TestRejectedOpsDoNotConsumeIDs(t *testing.T) {
	m := New()
	if _, err := m.Register(nil); errCode(err) != ErrInvalidArgument {
		t.Fatalf("empty batch: %v", err)
	}
	if _, err := m.Register([]Doc{{Key: ""}}); errCode(err) != ErrInvalidArgument {
		t.Fatalf("empty key: %v", err)
	}
	if _, err := m.Register([]Doc{{Key: "k", Terms: []string{""}}}); errCode(err) != ErrInvalidArgument {
		t.Fatalf("empty term: %v", err)
	}
	a, _ := m.Register([]Doc{{Key: "k"}})
	if a != 1 {
		t.Fatalf("rejected register consumed id: a=%d", a)
	}
	if _, err := m.Register([]Doc{{Key: "k"}}); errCode(err) != ErrKeyConflict {
		t.Fatalf("duplicate live key: %v", err)
	}
	if _, err := m.Register([]Doc{{Key: "x"}, {Key: "x"}}); errCode(err) != ErrKeyConflict {
		t.Fatalf("dup within batch: %v", err)
	}
	b, _ := m.Register([]Doc{{Key: "j"}})
	if b != 2 {
		t.Fatalf("rejected register consumed id: b=%d", b)
	}

	if _, err := m.BeginMerge([]int{a}); errCode(err) != ErrInvalidArgument {
		t.Fatalf("single id: %v", err)
	}
	if _, err := m.BeginMerge([]int{a, a}); errCode(err) != ErrInvalidArgument {
		t.Fatalf("dup ids: %v", err)
	}
	if _, err := m.BeginMerge([]int{a, 999}); errCode(err) != ErrSegmentNotFound {
		t.Fatalf("missing segment: %v", err)
	}
	h, err := m.BeginMerge([]int{a, b})
	if err != nil || h != 1 {
		t.Fatalf("first handle must be 1, got %d %v", h, err)
	}
	if _, err := m.BeginMerge([]int{a, b}); errCode(err) != ErrSegmentBusy {
		t.Fatalf("busy: %v", err)
	}
	if err := m.Delete("nope"); errCode(err) != ErrKeyNotFound {
		t.Fatalf("delete missing: %v", err)
	}
	if _, err := m.Stats(999); errCode(err) != ErrSegmentNotFound {
		t.Fatalf("stats missing: %v", err)
	}
	if _, err := m.Postings(999, "t"); errCode(err) != ErrSegmentNotFound {
		t.Fatalf("postings missing: %v", err)
	}
	if _, err := m.Commit(999); errCode(err) != ErrInvalidHandle {
		t.Fatalf("commit bad handle: %v", err)
	}
	// 拒绝发生后提交仍取段号 3（而非更大值），句柄号也未被消耗。
	id, err := m.Commit(h)
	if err != nil || id != 3 {
		t.Fatalf("commit after rejects: id=%d err=%v", id, err)
	}
	if _, err := m.BeginMerge(nil); errCode(err) != ErrInvalidArgument {
		t.Fatalf("begin nil: %v", err)
	}
}

// 返回值是副本：修改统计与倒排表不影响内部状态。
func TestReturnedValuesAreCopies(t *testing.T) {
	m := New()
	id, _ := m.Register([]Doc{{Key: "a", Terms: []string{"x", "x"}}})
	st, _ := m.Stats(id)
	st.MaxDoc = 999
	st.NumDocs = 999
	st.TermCount = 999
	if got := mustStats(t, m, id); got != (Stats{MaxDoc: 1, NumDocs: 1, TermCount: 1}) {
		t.Fatalf("stats mutated: %+v", got)
	}
	list, _ := m.Postings(id, "x")
	list[0].Doc = 50
	list[0].TF = 50
	list[0].Positions[0] = 50
	got := mustPostings(t, m, id, "x")
	want := []Posting{{Doc: 0, TF: 2, Positions: []int{0, 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postings mutated: %+v", got)
	}
}
