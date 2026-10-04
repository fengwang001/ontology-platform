package engine

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/searcher"
)

func mustIndex(t *testing.T, e *Engine, id, body string, ifSeq int64) int64 {
	t.Helper()
	seq, err := e.Index([]byte(id), []byte(body), ifSeq)
	if err != nil {
		t.Fatalf("Index(%q,%q,%d) err = %v", id, body, ifSeq, err)
	}
	return seq
}

func mustDelete(t *testing.T, e *Engine, id string) int64 {
	t.Helper()
	seq, err := e.Delete([]byte(id))
	if err != nil {
		t.Fatalf("Delete(%q) err = %v", id, err)
	}
	return seq
}

func mustGet(t *testing.T, e *Engine, id string) (string, int64) {
	t.Helper()
	body, seq, err := e.Get([]byte(id))
	if err != nil {
		t.Fatalf("Get(%q) err = %v", id, err)
	}
	return string(body), seq
}

func mustSearch(t *testing.T, e *Engine) map[string]string {
	t.Helper()
	docs, err := e.Search()
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	out := make(map[string]string, len(docs))
	for _, doc := range docs {
		out[string(doc.ID)] = string(doc.Body)
	}
	return out
}

func searchIDs(t *testing.T, e *Engine) []string {
	t.Helper()
	docs, err := e.Search()
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = string(doc.ID)
	}
	return ids
}

func mustCrashRecover(t *testing.T, e *Engine) int {
	t.Helper()
	if err := e.Crash(); err != nil {
		t.Fatalf("Crash() err = %v", err)
	}
	n, err := e.Recover()
	if err != nil {
		t.Fatalf("Recover() err = %v", err)
	}
	return n
}

func equalIDs(got []string, want ...string) bool {
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

// TestAsyncExample 复现题目 Async 示例：刷新过但未落盘的操作崩溃后消失，
// 未落盘的删除一并丢失，序号被重新分配。
func TestAsyncExample(t *testing.T) {
	e := New()
	if seq := mustIndex(t, e, "a", "A", -1); seq != 1 {
		t.Fatalf("seq = %d, want 1", seq)
	}
	if seq := mustIndex(t, e, "b", "B", -1); seq != 2 {
		t.Fatalf("seq = %d, want 2", seq)
	}
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	if e.committed != 2 || e.synced != 2 || e.maxSeq != 2 {
		t.Fatalf("after Flush: committed=%d synced=%d maxSeq=%d", e.committed, e.synced, e.maxSeq)
	}
	if e.tl.Generation() != 2 || e.tl.Len() != 0 {
		t.Fatalf("after Flush: gen=%d uncommitted=%d", e.tl.Generation(), e.tl.Len())
	}
	mustIndex(t, e, "c", "C", -1) // seq 3
	if err := e.Sync(); err != nil {
		t.Fatal(err)
	}
	if e.synced != 3 {
		t.Fatalf("synced = %d, want 3", e.synced)
	}
	mustIndex(t, e, "d", "D", -1) // seq 4
	if err := e.Refresh(); err != nil {
		t.Fatal(err)
	}
	if ids := searchIDs(t, e); !equalIDs(ids, "a", "b", "c", "d") {
		t.Fatalf("Search ids = %v", ids)
	}
	mustDelete(t, e, "a") // seq 5
	if _, _, err := e.Get([]byte("a")); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("Get(a) err = %v, want ErrDocumentNotFound", err)
	}
	if got := mustSearch(t, e); got["a"] != "A" {
		t.Fatalf("Search should still see a, got %v", got)
	}
	if n := mustCrashRecover(t, e); n != 1 {
		t.Fatalf("replayed = %d, want 1", n)
	}
	if e.replayed != 1 {
		t.Fatalf("replayed counter = %d, want 1", e.replayed)
	}
	if ids := searchIDs(t, e); !equalIDs(ids, "a", "b", "c") {
		t.Fatalf("Search ids after recover = %v", ids)
	}
	if _, _, err := e.Get([]byte("d")); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("Get(d) err = %v, want ErrDocumentNotFound", err)
	}
	if seq := mustIndex(t, e, "e", "E", -1); seq != 4 {
		t.Fatalf("next seq = %d, want 4 (reused)", seq)
	}
	if !(e.committed <= e.synced && e.synced <= e.maxSeq) {
		t.Fatalf("invariant broken: %d %d %d", e.committed, e.synced, e.maxSeq)
	}
}

// TestRequestExample 复现题目 Request 示例：每个写操作返回前落盘。
func TestRequestExample(t *testing.T) {
	e := New()
	if err := e.SetDurability(Request); err != nil {
		t.Fatal(err)
	}
	mustIndex(t, e, "a", "A", -1)
	mustIndex(t, e, "b", "B", -1)
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	mustIndex(t, e, "c", "C", -1)
	mustIndex(t, e, "d", "D", -1)
	if err := e.Refresh(); err != nil {
		t.Fatal(err)
	}
	mustDelete(t, e, "a")
	if e.synced != 5 {
		t.Fatalf("synced = %d, want 5", e.synced)
	}
	if n := mustCrashRecover(t, e); n != 3 {
		t.Fatalf("replayed = %d, want 3", n)
	}
	if ids := searchIDs(t, e); !equalIDs(ids, "b", "c", "d") {
		t.Fatalf("Search ids after recover = %v", ids)
	}
	if seq := mustIndex(t, e, "e", "E", -1); seq != 6 {
		t.Fatalf("next seq = %d, want 6", seq)
	}
}

// TestConditionalIndex 复现题目条件写示例：ifSeq 按实时状态判定。
func TestConditionalIndex(t *testing.T) {
	e := New()
	mustIndex(t, e, "a", "v1", -1) // seq 1
	if err := e.Refresh(); err != nil {
		t.Fatal(err)
	}
	mustIndex(t, e, "a", "v2", 1) // seq 2
	if got := mustSearch(t, e); got["a"] != "v1" {
		t.Fatalf("Search should still see v1, got %v", got)
	}
	if body, seq := mustGet(t, e, "a"); body != "v2" || seq != 2 {
		t.Fatalf("Get(a) = %q seq %d, want v2 seq 2", body, seq)
	}
	if _, err := e.Index([]byte("a"), []byte("v3"), 1); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Index(a,v3,1) err = %v, want ErrVersionConflict", err)
	}
	mustDelete(t, e, "a") // seq 3
	if _, err := e.Index([]byte("a"), []byte("v4"), 3); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Index(a,v4,3) err = %v, want ErrVersionConflict", err)
	}
	mustIndex(t, e, "a", "v4", -1) // seq 4
	if n := mustCrashRecover(t, e); n != 0 {
		t.Fatalf("replayed = %d, want 0 (never synced)", n)
	}
	if _, _, err := e.Get([]byte("a")); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("Get(a) err = %v, want ErrDocumentNotFound", err)
	}
	if seq := mustIndex(t, e, "z", "Z", -1); seq != 1 {
		t.Fatalf("next seq = %d, want 1", seq)
	}
}

// TestSetDurabilityRequestSyncsImmediately 切到 Request 立即落盘一次。
func TestSetDurabilityRequestSyncsImmediately(t *testing.T) {
	e := New()
	mustIndex(t, e, "a", "A", -1)
	mustIndex(t, e, "b", "B", -1)
	if e.synced != 0 {
		t.Fatalf("synced = %d, want 0 before switch", e.synced)
	}
	if err := e.SetDurability(Request); err != nil {
		t.Fatal(err)
	}
	if e.synced != 2 {
		t.Fatalf("synced = %d, want 2 after switching to Request", e.synced)
	}
	if n := mustCrashRecover(t, e); n != 2 {
		t.Fatalf("replayed = %d, want 2", n)
	}
	if err := e.SetDurability(Durability(7)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetDurability(7) err = %v, want ErrInvalidArgument", err)
	}
}

// TestFlushThenCrashReplaysZero Flush 后重放数为 0。
func TestFlushThenCrashReplaysZero(t *testing.T) {
	e := New()
	mustIndex(t, e, "a", "A", -1)
	mustDelete(t, e, "a")
	mustIndex(t, e, "b", "B", -1)
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := mustCrashRecover(t, e); n != 0 {
		t.Fatalf("replayed = %d, want 0", n)
	}
	if ids := searchIDs(t, e); !equalIDs(ids, "b") {
		t.Fatalf("Search ids = %v", ids)
	}
	if e.tl.Len() != 0 {
		t.Fatalf("uncommitted = %d, want 0", e.tl.Len())
	}
}

// TestDoubleCrashRecoverIdempotent 连续两次崩溃恢复结果不变。
func TestDoubleCrashRecoverIdempotent(t *testing.T) {
	e := New()
	mustIndex(t, e, "a", "A", -1)
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	mustIndex(t, e, "b", "B", -1)
	mustIndex(t, e, "c", "C", -1)
	if err := e.Sync(); err != nil {
		t.Fatal(err)
	}
	mustIndex(t, e, "d", "D", -1) // 未落盘，崩溃丢失
	first := mustCrashRecover(t, e)
	if first != 2 {
		t.Fatalf("first replayed = %d, want 2", first)
	}
	want := mustSearch(t, e)
	second := mustCrashRecover(t, e)
	if second != first {
		t.Fatalf("second replayed = %d, want %d", second, first)
	}
	if got := mustSearch(t, e); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("second recover changed view: %v vs %v", got, want)
	}
	if ids := searchIDs(t, e); !equalIDs(ids, "a", "b", "c") {
		t.Fatalf("Search ids = %v", ids)
	}
}

// TestRejectionOrder 崩溃态拒绝次序：参数非法 > 未恢复 > 版本冲突/文档不存在。
func TestRejectionOrder(t *testing.T) {
	e := New()
	mustIndex(t, e, "a", "A", -1)
	if err := e.Crash(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid id beats crashed", func() error {
			_, err := e.Index(nil, []byte("x"), -1)
			return err
		}, ErrInvalidArgument},
		{"invalid body beats crashed", func() error {
			_, err := e.Index([]byte("a"), make([]byte, 65537), -1)
			return err
		}, ErrInvalidArgument},
		{"invalid ifSeq beats crashed", func() error {
			_, err := e.Index([]byte("a"), []byte("x"), -2)
			return err
		}, ErrInvalidArgument},
		{"invalid delete id beats crashed", func() error {
			_, err := e.Delete(make([]byte, 513))
			return err
		}, ErrInvalidArgument},
		{"invalid get id beats crashed", func() error {
			_, _, err := e.Get(nil)
			return err
		}, ErrInvalidArgument},
		{"index while crashed", func() error {
			_, err := e.Index([]byte("a"), []byte("x"), -1)
			return err
		}, ErrNotRecovered},
		{"delete while crashed", func() error {
			_, err := e.Delete([]byte("a"))
			return err
		}, ErrNotRecovered},
		{"get while crashed", func() error {
			_, _, err := e.Get([]byte("a"))
			return err
		}, ErrNotRecovered},
		{"search while crashed", func() error {
			_, err := e.Search()
			return err
		}, ErrNotRecovered},
		{"refresh while crashed", e.Refresh, ErrNotRecovered},
		{"sync while crashed", e.Sync, ErrNotRecovered},
		{"flush while crashed", e.Flush, ErrNotRecovered},
		{"set durability while crashed", func() error { return e.SetDurability(Request) }, ErrNotRecovered},
		{"crash while crashed", e.Crash, ErrNotRecovered},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if _, err := e.Recover(); err != nil {
		t.Fatalf("Recover() err = %v", err)
	}
	if _, err := e.Recover(); !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("Recover on healthy engine err = %v, want ErrStateMismatch", err)
	}
	// 恢复后版本冲突与文档不存在按序判定。
	if _, err := e.Index([]byte("a"), []byte("x"), 99); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}
	if _, err := e.Delete([]byte("ghost")); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("err = %v, want ErrDocumentNotFound", err)
	}
}

// TestRejectedOpsConsumeNothing 被拒绝的操作不占序号、不写日志、不落盘。
func TestRejectedOpsConsumeNothing(t *testing.T) {
	e := New()
	mustIndex(t, e, "a", "A", -1) // seq 1
	maxSeq, synced, logLen := e.maxSeq, e.synced, e.tl.Len()
	if _, err := e.Index([]byte("a"), []byte("x"), 7); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.Delete([]byte("ghost")); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.Index(nil, nil, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v", err)
	}
	if e.maxSeq != maxSeq || e.synced != synced || e.tl.Len() != logLen {
		t.Fatalf("rejected ops changed state: maxSeq %d->%d synced %d->%d log %d->%d",
			maxSeq, e.maxSeq, synced, e.synced, logLen, e.tl.Len())
	}
	if seq := mustIndex(t, e, "b", "B", -1); seq != 2 {
		t.Fatalf("next seq = %d, want 2", seq)
	}
}

// TestReplayCountIndependentOfCommittedSize replayed == synced - committed，
// 与已提交文档总数无关（1000 与 100000 两档对照）。
func TestReplayCountIndependentOfCommittedSize(t *testing.T) {
	for _, committedDocs := range []int{1000, 100000} {
		t.Run(fmt.Sprintf("committed=%d", committedDocs), func(t *testing.T) {
			e := New()
			for i := 0; i < committedDocs; i++ {
				id := fmt.Sprintf("doc-%06d", i)
				if _, err := e.Index([]byte(id), []byte("x"), -1); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.Flush(); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 10; i++ {
				id := fmt.Sprintf("pending-%d", i)
				if _, err := e.Index([]byte(id), []byte("y"), -1); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.Sync(); err != nil {
				t.Fatal(err)
			}
			n := mustCrashRecover(t, e)
			if n != 10 || e.replayed != 10 {
				t.Fatalf("replayed = %d (counter %d), want 10", n, e.replayed)
			}
			if int64(n) != e.synced-e.committed {
				t.Fatalf("replayed %d != synced-committed %d", n, e.synced-e.committed)
			}
			docs, err := e.Search()
			if err != nil {
				t.Fatal(err)
			}
			if len(docs) != committedDocs+10 {
				t.Fatalf("visible docs = %d, want %d", len(docs), committedDocs+10)
			}
		})
	}
}

// TestGetTouchesAtMostTwo Get 触碰的记录数不超过 2。
func TestGetTouchesAtMostTwo(t *testing.T) {
	e := New()
	mustIndex(t, e, "a", "A", -1)
	if err := e.Refresh(); err != nil {
		t.Fatal(err)
	}
	mustIndex(t, e, "b", "B", -1) // 留在版本表
	mustDelete(t, e, "a")         // 版本表中的删除
	for _, id := range []string{"a", "b", "ghost"} {
		_, _, _ = e.Get([]byte(id))
		if e.getTouches > 2 {
			t.Fatalf("Get(%q) touched %d records, want <= 2", id, e.getTouches)
		}
		t.Logf("Get(%q) touched %d records (<=2: version table + view)", id, e.getTouches)
	}
}

// ---- 逐步朴素模拟：按规则直接维护状态，与引擎逐步对照 ----

type mdoc struct {
	body string
	seq  int64
}

type mop struct {
	seq  int64
	del  bool
	id   string
	body string
}

type model struct {
	mode      Durability
	ops       []mop // 事务日志中存活的条目
	committed int64
	synced    int64
	maxSeq    int64
	cview     map[string]mdoc // 提交点
	view      map[string]mdoc // 搜索视图
	vtab      map[string]mop  // 版本表
	crashed   bool
}

func newModel() *model {
	return &model{
		mode:  Async,
		cview: map[string]mdoc{},
		view:  map[string]mdoc{},
		vtab:  map[string]mop{},
	}
}

func copyView(src map[string]mdoc) map[string]mdoc {
	out := make(map[string]mdoc, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (m *model) realtime(id string) (mdoc, bool) {
	if op, ok := m.vtab[id]; ok {
		if op.del {
			return mdoc{}, false
		}
		return mdoc{body: op.body, seq: op.seq}, true
	}
	d, ok := m.view[id]
	return d, ok
}

func (m *model) index(id, body string, ifSeq int64) (int64, error) {
	if m.crashed {
		return 0, ErrNotRecovered
	}
	if ifSeq != -1 {
		d, ok := m.realtime(id)
		if !ok || d.seq != ifSeq {
			return 0, ErrVersionConflict
		}
	}
	m.maxSeq++
	op := mop{seq: m.maxSeq, id: id, body: body}
	m.ops = append(m.ops, op)
	m.vtab[id] = op
	if m.mode == Request {
		m.synced = m.maxSeq
	}
	return m.maxSeq, nil
}

func (m *model) delete(id string) (int64, error) {
	if m.crashed {
		return 0, ErrNotRecovered
	}
	if _, ok := m.realtime(id); !ok {
		return 0, ErrDocumentNotFound
	}
	m.maxSeq++
	op := mop{seq: m.maxSeq, del: true, id: id}
	m.ops = append(m.ops, op)
	m.vtab[id] = op
	if m.mode == Request {
		m.synced = m.maxSeq
	}
	return m.maxSeq, nil
}

func (m *model) get(id string) (mdoc, error) {
	if m.crashed {
		return mdoc{}, ErrNotRecovered
	}
	d, ok := m.realtime(id)
	if !ok {
		return mdoc{}, ErrDocumentNotFound
	}
	return d, nil
}

func (m *model) refresh() error {
	if m.crashed {
		return ErrNotRecovered
	}
	for id, op := range m.vtab {
		if op.del {
			delete(m.view, id)
		} else {
			m.view[id] = mdoc{body: op.body, seq: op.seq}
		}
	}
	m.vtab = map[string]mop{}
	return nil
}

func (m *model) sync() error {
	if m.crashed {
		return ErrNotRecovered
	}
	m.synced = m.maxSeq
	return nil
}

func (m *model) flush() error {
	if m.crashed {
		return ErrNotRecovered
	}
	_ = m.refresh()
	m.cview = copyView(m.view)
	m.committed = m.maxSeq
	m.synced = m.maxSeq
	m.ops = nil // 日志换代
	return nil
}

func (m *model) setDurability(mode Durability) error {
	if m.crashed {
		return ErrNotRecovered
	}
	m.mode = mode
	if mode == Request {
		m.synced = m.maxSeq
	}
	return nil
}

func (m *model) crash() error {
	if m.crashed {
		return ErrNotRecovered
	}
	kept := m.ops[:0]
	for _, op := range m.ops {
		if op.seq <= m.synced {
			kept = append(kept, op)
		}
	}
	m.ops = kept
	m.vtab = map[string]mop{}
	m.view = nil
	m.crashed = true
	return nil
}

func (m *model) recover() (int, error) {
	if !m.crashed {
		return 0, ErrStateMismatch
	}
	m.view = copyView(m.cview)
	n := 0
	for _, op := range m.ops {
		if op.seq <= m.committed || op.seq > m.synced {
			continue
		}
		if op.del {
			delete(m.view, op.id)
		} else {
			m.view[op.id] = mdoc{body: op.body, seq: op.seq}
		}
		n++
	}
	m.maxSeq = m.synced
	m.crashed = false
	return n, nil
}

func sameErr(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

// TestRandomAgainstModel 随机序列在每个位置插入 Crash/Recover，
// 与逐步朴素模拟对照（1500 组），日志打印输入、输出与判定依据。
func TestRandomAgainstModel(t *testing.T) {
	const groups = 1500
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g)*7919 + 13))
		e := New()
		m := newModel()
		steps := 20 + rng.Intn(30)
		var trace []string
		step := func(format string, args ...any) {
			trace = append(trace, fmt.Sprintf(format, args...))
		}
		fail := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("group %d step %d: %s\ntrace: %s", g, len(trace),
				fmt.Sprintf(format, args...), strings.Join(trace, "; "))
		}
		for s := 0; s < steps; s++ {
			action := rng.Intn(100)
			switch {
			case action < 35: // Index
				id := ids[rng.Intn(len(ids))]
				body := fmt.Sprintf("v%d", s)
				ifSeq := int64(-1)
				if rng.Intn(2) == 0 {
					ifSeq = int64(rng.Intn(int(m.maxSeq)+2)) - 1
				}
				gotSeq, gotErr := e.Index([]byte(id), []byte(body), ifSeq)
				wantSeq, wantErr := m.index(id, body, ifSeq)
				step("Index(%s,%s,%d)->(%d,%v)", id, body, ifSeq, gotSeq, gotErr)
				if !sameErr(gotErr, wantErr) || (gotErr == nil && gotSeq != wantSeq) {
					fail("Index: got (%d,%v) want (%d,%v)", gotSeq, gotErr, wantSeq, wantErr)
				}
			case action < 50: // Delete
				id := ids[rng.Intn(len(ids))]
				gotSeq, gotErr := e.Delete([]byte(id))
				wantSeq, wantErr := m.delete(id)
				step("Delete(%s)->(%d,%v)", id, gotSeq, gotErr)
				if !sameErr(gotErr, wantErr) || (gotErr == nil && gotSeq != wantSeq) {
					fail("Delete: got (%d,%v) want (%d,%v)", gotSeq, gotErr, wantSeq, wantErr)
				}
			case action < 60: // Get
				id := ids[rng.Intn(len(ids))]
				gotBody, gotSeq, gotErr := e.Get([]byte(id))
				want, wantErr := m.get(id)
				step("Get(%s)->(%q,%d,%v)", id, gotBody, gotSeq, gotErr)
				if !sameErr(gotErr, wantErr) {
					fail("Get: got err %v want %v", gotErr, wantErr)
				}
				if gotErr == nil && (string(gotBody) != want.body || gotSeq != want.seq) {
					fail("Get: got (%q,%d) want (%q,%d)", gotBody, gotSeq, want.body, want.seq)
				}
			case action < 68: // Refresh
				gotErr := e.Refresh()
				wantErr := m.refresh()
				step("Refresh->%v", gotErr)
				if !sameErr(gotErr, wantErr) {
					fail("Refresh: got %v want %v", gotErr, wantErr)
				}
			case action < 74: // Sync
				gotErr := e.Sync()
				wantErr := m.sync()
				step("Sync->%v", gotErr)
				if !sameErr(gotErr, wantErr) {
					fail("Sync: got %v want %v", gotErr, wantErr)
				}
			case action < 80: // Flush
				gotErr := e.Flush()
				wantErr := m.flush()
				step("Flush->%v", gotErr)
				if !sameErr(gotErr, wantErr) {
					fail("Flush: got %v want %v", gotErr, wantErr)
				}
			case action < 84: // SetDurability
				mode := Durability(rng.Intn(2))
				gotErr := e.SetDurability(mode)
				wantErr := m.setDurability(mode)
				step("SetDurability(%d)->%v", mode, gotErr)
				if !sameErr(gotErr, wantErr) {
					fail("SetDurability: got %v want %v", gotErr, wantErr)
				}
			case action < 92: // Crash + Recover（偶尔连续两次）
				rounds := 1
				if rng.Intn(3) == 0 {
					rounds = 2
				}
				for r := 0; r < rounds; r++ {
					gotCErr := e.Crash()
					wantCErr := m.crash()
					gotN, gotRErr := e.Recover()
					wantN, wantRErr := m.recover()
					step("Crash->%v Recover->(%d,%v)", gotCErr, gotN, gotRErr)
					if !sameErr(gotCErr, wantCErr) || !sameErr(gotRErr, wantRErr) {
						fail("Crash/Recover err: got (%v,%v) want (%v,%v)",
							gotCErr, gotRErr, wantCErr, wantRErr)
					}
					if gotRErr == nil && gotN != wantN {
						fail("Recover replayed: got %d want %d", gotN, wantN)
					}
				}
			default: // Search 全量比对
				gotDocs, gotErr := e.Search()
				step("Search->(%d docs,%v)", len(gotDocs), gotErr)
				if m.crashed {
					if !errors.Is(gotErr, ErrNotRecovered) {
						fail("Search: got %v want ErrNotRecovered", gotErr)
					}
					continue
				}
				if gotErr != nil {
					fail("Search: got err %v", gotErr)
				}
				want := make([]searcher.Doc, 0, len(m.view))
				for id, d := range m.view {
					want = append(want, searcher.Doc{ID: []byte(id), Body: []byte(d.body), Seq: d.seq})
				}
				sort.Slice(want, func(i, j int) bool { return string(want[i].ID) < string(want[j].ID) })
				if len(gotDocs) != len(want) {
					fail("Search: got %d docs want %d", len(gotDocs), len(want))
				}
				for i := range want {
					if string(gotDocs[i].ID) != string(want[i].ID) ||
						string(gotDocs[i].Body) != string(want[i].Body) ||
						gotDocs[i].Seq != want[i].Seq {
						fail("Search[%d]: got %+v want %+v", i, gotDocs[i], want[i])
					}
				}
			}
			// 不变量：committed <= synced <= maxSeq（崩溃态下 maxSeq 未回退，只查水位关系）
			if !m.crashed && !(m.committed <= m.synced && m.synced <= m.maxSeq) {
				fail("model invariant broken: %d %d %d", m.committed, m.synced, m.maxSeq)
			}
			if !e.crashed && !(e.committed <= e.synced && e.synced <= e.maxSeq) {
				fail("engine invariant broken: %d %d %d", e.committed, e.synced, e.maxSeq)
			}
			if e.maxSeq != m.maxSeq || e.synced != m.synced || e.committed != m.committed {
				fail("watermarks: engine (%d,%d,%d) model (%d,%d,%d)",
					e.committed, e.synced, e.maxSeq, m.committed, m.synced, m.maxSeq)
			}
		}
		// 判定依据：恢复后的实时状态 == 按序应用 seq<=synced 的全部存活操作；
		// 搜索视图 == 提交点 + 已刷新/重放操作；水位三元组逐步相等。
		t.Logf("group %d ok: steps=%d final(committed=%d synced=%d maxSeq=%d docs=%d) "+
			"basis: realtime=apply(seq<=synced) view=commit+refreshed replay=(committed,synced]\nops: %s",
			g, steps, m.committed, m.synced, m.maxSeq, len(m.view), strings.Join(trace, "; "))
	}
}

// TestConcurrentSmoke 并发调用等价于某个串行顺序（配合 -race）。
func TestConcurrentSmoke(t *testing.T) {
	e := New()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("id-%d", w%4)
			for i := 0; i < 200; i++ {
				switch i % 7 {
				case 0:
					_, _ = e.Index([]byte(id), []byte("x"), -1)
				case 1:
					_, _ = e.Delete([]byte(id))
				case 2:
					_, _, _ = e.Get([]byte(id))
				case 3:
					_ = e.Refresh()
				case 4:
					_ = e.Sync()
				case 5:
					_, _ = e.Search()
				case 6:
					_ = e.Flush()
				}
			}
		}(w)
	}
	wg.Wait()
	if !(e.committed <= e.synced && e.synced <= e.maxSeq) {
		t.Fatalf("invariant broken: %d %d %d", e.committed, e.synced, e.maxSeq)
	}
	if err := e.Crash(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Recover(); err != nil {
		t.Fatal(err)
	}
	if e.maxSeq != e.synced {
		t.Fatalf("after recover maxSeq=%d synced=%d", e.maxSeq, e.synced)
	}
}
