package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustRegister(t *testing.T, m *Merger, batch []Doc) int {
	t.Helper()
	id, err := m.Register(batch)
	if err != nil {
		t.Fatalf("Register(%v) unexpected error: %v", batch, err)
	}
	return id
}

func expectErr(t *testing.T, got error, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func expectStats(t *testing.T, m *Merger, segID int, want Stats) {
	t.Helper()
	got, err := m.Stats(segID)
	if err != nil {
		t.Fatalf("Stats(%d) error: %v", segID, err)
	}
	if got != want {
		t.Fatalf("Stats(%d) = %+v, want %+v", segID, got, want)
	}
}

func allPostings(t *testing.T, m *Merger, segID int, terms []string) map[string][]Posting {
	t.Helper()
	out := make(map[string][]Posting, len(terms))
	for _, term := range terms {
		p, err := m.Postings(segID, term)
		if err != nil {
			t.Fatalf("Postings(%d,%q) error: %v", segID, term, err)
		}
		out[term] = p
	}
	return out
}

// ids 次序不同导致新文档编号不同。
func TestMergeIDOrderChangesNumbering(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"x"}}, {Key: "b", Terms: []string{"y"}}})
	s2 := mustRegister(t, m, []Doc{{Key: "c", Terms: []string{"x"}}})

	h, err := m.BeginMerge([]int{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	expectStats(t, m, merged, Stats{MaxDoc: 3, NumDocs: 3, TermCount: 2})
	if got, _ := m.Postings(merged, "x"); !reflect.DeepEqual(got, []Posting{
		{DocID: 0, TF: 1, Positions: []int{0}},
		{DocID: 2, TF: 1, Positions: []int{0}},
	}) {
		t.Fatalf("order s1,s2 postings = %+v", got)
	}

	m2 := NewMerger()
	ss1 := mustRegister(t, m2, []Doc{{Key: "a", Terms: []string{"x"}}, {Key: "b", Terms: []string{"y"}}})
	ss2 := mustRegister(t, m2, []Doc{{Key: "c", Terms: []string{"x"}}})
	h2, err := m2.BeginMerge([]int{ss2, ss1})
	if err != nil {
		t.Fatal(err)
	}
	merged2, err := m2.Commit(h2)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m2.Postings(merged2, "x"); !reflect.DeepEqual(got, []Posting{
		{DocID: 0, TF: 1, Positions: []int{0}},
		{DocID: 1, TF: 1, Positions: []int{0}},
	}) {
		t.Fatalf("order s2,s1 postings = %+v", got)
	}
}

// 合并时全部文档已删除：maxDoc 为 0（BeginMerge 冻结到空序列）。
func TestMergeAllDeletedGivesEmptySegment(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"x"}}})
	s2 := mustRegister(t, m, []Doc{{Key: "b", Terms: []string{"y", "z"}}})
	if err := m.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("b"); err != nil {
		t.Fatal(err)
	}
	h, err := m.BeginMerge([]int{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	expectStats(t, m, merged, Stats{MaxDoc: 0, NumDocs: 0, TermCount: 0})
	for _, term := range []string{"x", "y", "z"} {
		p, err := m.Postings(merged, term)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 0 {
			t.Fatalf("term %q postings = %+v, want empty", term, p)
		}
	}
}

// BeginMerge 之后删除再 Commit：maxDoc 保留，numDocs/df/termCount 减少。
func TestDeleteBetweenBeginAndCommit(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"x", "x"}}})
	s2 := mustRegister(t, m, []Doc{
		{Key: "b", Terms: []string{"x", "y"}},
		{Key: "c", Terms: []string{"z"}},
	})
	h, err := m.BeginMerge([]int{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("b"); err != nil {
		t.Fatal(err)
	}
	merged, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	expectStats(t, m, merged, Stats{MaxDoc: 3, NumDocs: 2, TermCount: 2}) // 存活 a,c：x,z
	wantX := []Posting{{DocID: 0, TF: 2, Positions: []int{0, 1}}}
	if got, _ := m.Postings(merged, "x"); !reflect.DeepEqual(got, wantX) {
		t.Fatalf("x postings = %+v, want %+v", got, wantX)
	}
	if got, _ := m.Postings(merged, "y"); len(got) != 0 {
		t.Fatalf("y postings = %+v, want empty", got)
	}
	if got, _ := m.Postings(merged, "z"); !reflect.DeepEqual(got, []Posting{
		{DocID: 2, TF: 1, Positions: []int{0}},
	}) {
		t.Fatalf("z postings = %+v", got)
	}
	// 输入段已移除。
	if _, err := m.Stats(s1); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatalf("old segment still present")
	}
}

// 删除后同键重新登记再 Commit：旧冻结文档不复活，新文档不受影响。
func TestReregisterSameKeyBeforeCommit(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"old"}}})
	s2 := mustRegister(t, m, []Doc{{Key: "b", Terms: []string{"b"}}})
	h, err := m.BeginMerge([]int{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("a"); err != nil {
		t.Fatal(err)
	}
	s3 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"new"}}})
	merged, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	expectStats(t, m, merged, Stats{MaxDoc: 2, NumDocs: 1, TermCount: 1})
	if got, _ := m.Postings(merged, "old"); len(got) != 0 {
		t.Fatalf("old postings = %+v, want empty", got)
	}
	if got, _ := m.Postings(merged, "new"); len(got) != 0 {
		t.Fatalf("new postings leaked into merged segment: %+v", got)
	}
	expectStats(t, m, s3, Stats{MaxDoc: 1, NumDocs: 1, TermCount: 1})
	if got, _ := m.Postings(s3, "new"); !reflect.DeepEqual(got, []Posting{
		{DocID: 0, TF: 1, Positions: []int{0}},
	}) {
		t.Fatalf("new doc postings = %+v", got)
	}
	// 存活键 a 仍解析到 s3。
	if err := m.Delete("a"); err != nil {
		t.Fatalf("delete re-registered key: %v", err)
	}
	expectErr(t, m.Delete("a"), ErrKeyNotFound)
}

// 键恰在 BeginMerge 与 Commit 之间被删除并重新登记：冻结文档删除，新文档存活。
func TestDeleteAndReregisterBetweenBeginAndCommit(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"t1"}}, {Key: "b", Terms: []string{"t2"}}})
	s2 := mustRegister(t, m, []Doc{{Key: "c", Terms: []string{"t1"}}})
	h, err := m.BeginMerge([]int{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("a"); err != nil {
		t.Fatal(err)
	}
	s3 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"t3"}}})
	merged, err := m.Commit(h)
	if err != nil {
		t.Fatal(err)
	}
	expectStats(t, m, merged, Stats{MaxDoc: 3, NumDocs: 2, TermCount: 2}) // b,c：t2,t1
	if got, _ := m.Postings(merged, "t1"); !reflect.DeepEqual(got, []Posting{
		{DocID: 2, TF: 1, Positions: []int{0}},
	}) {
		t.Fatalf("t1 postings = %+v", got)
	}
	if got, _ := m.Postings(merged, "t3"); len(got) != 0 {
		t.Fatalf("t3 leaked: %+v", got)
	}
	expectStats(t, m, s3, Stats{MaxDoc: 1, NumDocs: 1, TermCount: 1})
	// 全局唯一键约束仍成立。
	_, err = m.Register([]Doc{{Key: "a"}})
	expectErr(t, err, ErrDuplicateKey)
}

// 同一段不能同时处于两个合并；Abort 后可再次合并。
func TestBusyAndAbortRetry(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a"}})
	s2 := mustRegister(t, m, []Doc{{Key: "b"}})
	s3 := mustRegister(t, m, []Doc{{Key: "c"}})

	h1, err := m.BeginMerge([]int{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.BeginMerge([]int{s2, s3})
	expectErr(t, err, ErrSegmentBusy)
	_, err = m.BeginMerge([]int{s1, s3})
	expectErr(t, err, ErrSegmentBusy)

	if err := m.Abort(h1); err != nil {
		t.Fatal(err)
	}
	// 已放弃的句柄不可再用。
	_, err = m.Commit(h1)
	expectErr(t, err, ErrInvalidHandle)
	expectErr(t, m.Abort(h1), ErrInvalidHandle)

	h2, err := m.BeginMerge([]int{s1, s3})
	if err != nil {
		t.Fatalf("merge after abort: %v", err)
	}
	merged, err := m.Commit(h2)
	if err != nil {
		t.Fatal(err)
	}
	expectStats(t, m, merged, Stats{MaxDoc: 2, NumDocs: 2, TermCount: 0})
	if _, err := m.Stats(s2); err != nil {
		t.Fatalf("s2 should survive aborted merge: %v", err)
	}
}

// 被拒绝的操作不消耗段编号或句柄号。
func TestRejectionsDoNotConsumeIDs(t *testing.T) {
	m := NewMerger()
	// 登记拒绝：空批、空键、空词项、冲突。
	if _, err := m.Register(nil); !errors.Is(err, ErrEmptyBatch) {
		t.Fatal(err)
	}
	if _, err := m.Register([]Doc{{Key: ""}}); !errors.Is(err, ErrEmptyKey) {
		t.Fatal(err)
	}
	if _, err := m.Register([]Doc{{Key: "k", Terms: []string{""}}}); !errors.Is(err, ErrEmptyTerm) {
		t.Fatal(err)
	}
	s1 := mustRegister(t, m, []Doc{{Key: "k", Terms: []string{"x"}}})
	if s1 != 1 {
		t.Fatalf("first successful segment id = %d, want 1", s1)
	}
	if _, err := m.Register([]Doc{{Key: "k"}}); !errors.Is(err, ErrDuplicateKey) {
		t.Fatal(err)
	}
	s2 := mustRegister(t, m, []Doc{{Key: "q"}})
	if s2 != 2 {
		t.Fatalf("second segment id = %d, want 2", s2)
	}

	// BeginMerge 拒绝：参数非法、段不存在、段忙；均不消耗句柄号。
	if _, err := m.BeginMerge([]int{s1}); !errors.Is(err, ErrInvalidMerge) {
		t.Fatal(err)
	}
	if _, err := m.BeginMerge([]int{s1, s1}); !errors.Is(err, ErrInvalidMerge) {
		t.Fatal(err)
	}
	if _, err := m.BeginMerge([]int{s1, 99}); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatal(err)
	}
	h1, err := m.BeginMerge([]int{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	if h1 != 1 {
		t.Fatalf("first handle = %d, want 1", h1)
	}
	if _, err := m.BeginMerge([]int{s1, s2}); !errors.Is(err, ErrSegmentBusy) {
		t.Fatal(err)
	}
	// Commit 拒绝不消耗下一个句柄号。
	if _, err := m.Commit(42); !errors.Is(err, ErrInvalidHandle) {
		t.Fatal(err)
	}
	if err := m.Abort(h1); err != nil {
		t.Fatal(err)
	}
	h2, err := m.BeginMerge([]int{s1, s2})
	if err != nil {
		t.Fatal(err)
	}
	if h2 != 2 {
		t.Fatalf("handle after rejected attempts = %d, want 2", h2)
	}
	merged, err := m.Commit(h2)
	if err != nil {
		t.Fatal(err)
	}
	if merged != 3 {
		t.Fatalf("merged segment id = %d, want 3", merged)
	}
}

// 拒绝原因优先级与查询错误。
func TestRejectionOrderingAndQueries(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a"}})
	_ = s1

	// 登记：空批优先于空键；空键优先于空词项；空词项优先于冲突。
	if _, err := m.Register([]Doc{{Key: "", Terms: []string{""}}}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if _, err := m.Register([]Doc{{Key: "a", Terms: []string{""}}}); !errors.Is(err, ErrEmptyTerm) {
		t.Fatalf("want ErrEmptyTerm, got %v", err)
	}
	// BeginMerge：参数非法优先于段不存在；段不存在优先于段忙。
	if _, err := m.BeginMerge([]int{7, 7}); !errors.Is(err, ErrInvalidMerge) {
		t.Fatalf("want ErrInvalidMerge, got %v", err)
	}
	if _, err := m.BeginMerge([]int{7, s1}); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatalf("want ErrSegmentNotFound, got %v", err)
	}
	if err := m.Delete("nope"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatal(err)
	}
	if _, err := m.Stats(7); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatal(err)
	}
	if _, err := m.Postings(7, "x"); !errors.Is(err, ErrSegmentNotFound) {
		t.Fatal(err)
	}
}

// 返回值是副本：修改不影响内部状态。
func TestReturnedValuesAreCopies(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"x", "x"}}})
	batch := []Doc{{Key: "b", Terms: []string{"y"}}}
	// 传入切片后续被修改不影响已建段。
	batch[0].Terms[0] = "mutated"
	st, _ := m.Stats(s1)
	st.MaxDoc = 999
	st2, _ := m.Stats(s1)
	if st2.MaxDoc != 1 {
		t.Fatalf("stats internal state mutated: %+v", st2)
	}
	p, _ := m.Postings(s1, "x")
	p[0].Positions[0] = 42
	p[0].DocID = 99
	p2, _ := m.Postings(s1, "x")
	if !reflect.DeepEqual(p2, []Posting{{DocID: 0, TF: 2, Positions: []int{0, 1}}}) {
		t.Fatalf("postings internal state mutated: %+v", p2)
	}
}

// 空 terms 文档；删除后同键可再登记。
func TestEmptyTermsAndReuseDeletedKey(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{{Key: "a", Terms: nil}})
	expectStats(t, m, s1, Stats{MaxDoc: 1, NumDocs: 1, TermCount: 0})
	if p, _ := m.Postings(s1, "anything"); len(p) != 0 {
		t.Fatalf("empty-terms doc postings = %+v", p)
	}
	if err := m.Delete("a"); err != nil {
		t.Fatal(err)
	}
	s2 := mustRegister(t, m, []Doc{{Key: "a", Terms: []string{"z"}}})
	if s2 != 2 {
		t.Fatalf("reused key segment id = %d, want 2", s2)
	}
	expectStats(t, m, s2, Stats{MaxDoc: 1, NumDocs: 1, TermCount: 1})
}

// 没有合并期间删除时，合并段与把冻结序列直接登记的朴素段逐字段相同。
func TestMergeEqualsNaiveRegister(t *testing.T) {
	m := NewMerger()
	s1 := mustRegister(t, m, []Doc{
		{Key: "a", Terms: []string{"x", "y", "x"}},
		{Key: "b", Terms: nil},
	})
	s2 := mustRegister(t, m, []Doc{{Key: "c", Terms: []string{"y", "z"}}})
	h, _ := m.BeginMerge([]int{s2, s1})
	merged, _ := m.Commit(h)

	naive := NewMerger()
	nSeg := mustRegister(t, naive, []Doc{
		{Key: "c", Terms: []string{"y", "z"}},
		{Key: "a", Terms: []string{"x", "y", "x"}},
		{Key: "b", Terms: nil},
	})
	terms := []string{"x", "y", "z", "missing"}
	sm, _ := m.Stats(merged)
	sn, _ := naive.Stats(nSeg)
	if sm != sn {
		t.Fatalf("stats differ: merged=%+v naive=%+v", sm, sn)
	}
	pm := allPostings(t, m, merged, terms)
	pn := allPostings(t, naive, nSeg, terms)
	if !reflect.DeepEqual(pm, pn) {
		t.Fatalf("postings differ:\nmerged=%v\nnaive =%v", pm, pn)
	}
}

// 并发压力：串行等价不变量——numDocs 之和 == 存活键个数。
func TestConcurrentInvariant(t *testing.T) {
	m := NewMerger()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				key := fmt.Sprintf("g%d-k%d", g, i)
				_, _ = m.Register([]Doc{{Key: key, Terms: []string{"t", fmt.Sprintf("g%d", g)}}})
				_ = m.Delete(key)
				_, _ = m.Register([]Doc{{Key: key, Terms: []string{"t2"}}})
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			id, err := m.Register([]Doc{{Key: fmt.Sprintf("merge-%d", i), Terms: []string{"m"}}})
			if err != nil {
				continue
			}
			id2, err := m.Register([]Doc{{Key: fmt.Sprintf("merge-%d-b", i), Terms: []string{"m"}}})
			if err != nil {
				continue
			}
			h, err := m.BeginMerge([]int{id, id2})
			if err != nil {
				continue
			}
			_, _ = m.Commit(h)
		}
	}()
	wg.Wait()

	m.mu.Lock()
	total := 0
	for _, seg := range m.segments {
		total += seg.numDocs()
		if total < 0 {
		}
	}
	live := len(m.liveKeys)
	m.mu.Unlock()
	if total != live {
		t.Fatalf("numDocs sum = %d, live keys = %d", total, live)
	}
}
