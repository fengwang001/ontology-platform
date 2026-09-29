package lag

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

func newLoggedEngine(t *testing.T, opts ...Option) *Engine {
	t.Helper()
	log := func(format string, args ...any) { t.Logf(format, args...) }
	opts = append([]Option{WithLogger(log)}, opts...)
	return New(opts...)
}

func mustInsert(t *testing.T, e *Engine, r Row) {
	t.Helper()
	if _, err := e.Insert(r); err != nil {
		t.Fatalf("insert %s/%s: %v", r.Partition, r.ID, err)
	}
}

// replayView 是下游按序应用变更日志后得到的视图，独立于引擎实现。
type replayView map[string]map[string]RowView

func (v replayView) ensure(p string) map[string]RowView {
	if v[p] == nil {
		v[p] = map[string]RowView{}
	}
	return v[p]
}

func applyLog(v replayView, changes []Change) {
	for _, c := range changes {
		switch c.Kind {
		case ChangeInsert:
			v.ensure(c.Partition)[c.ID] = RowView{
				Row:     Row{Partition: c.Partition, ID: c.ID, SortKey: c.SortKey, Value: c.Value},
				HasPrev: c.HasPrev, Prev: c.Prev}
		case ChangeUpdate:
			rv := v[c.Partition][c.ID]
			rv.HasPrev, rv.Prev = c.HasPrev, c.Prev
			v.ensure(c.Partition)[c.ID] = rv
		case ChangeDelete:
			delete(v[c.Partition], c.ID)
		}
	}
}

// batchRecompute 从一组行独立批量重算前驱值，按分区名、(SortKey, ID) 双升序。
func batchRecompute(rows []Row) []RowView {
	sorted := append([]Row(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Partition != sorted[j].Partition {
			return sorted[i].Partition < sorted[j].Partition
		}
		return lessKey(sorted[i].SortKey, sorted[i].ID, sorted[j].SortKey, sorted[j].ID)
	})
	out := make([]RowView, 0, len(sorted))
	for i, r := range sorted {
		v := RowView{Row: r}
		if i > 0 && sorted[i-1].Partition == r.Partition {
			v.HasPrev, v.Prev = true, sorted[i-1].Value
		}
		out = append(out, v)
	}
	return out
}

func sameViews(a, b []RowView) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 基本前驱：插入顺序 3,1,2 后前驱链正确，首行前驱严格为空。
func TestInsertBasicPrev(t *testing.T) {
	e := newLoggedEngine(t)
	stream := []Row{
		{Partition: "p", ID: "c", SortKey: 30, Value: "v30"},
		{Partition: "p", ID: "a", SortKey: 10, Value: "v10"},
		{Partition: "p", ID: "b", SortKey: 20, Value: "v20"},
	}
	replay := replayView{}
	for _, r := range stream {
		ch, err := e.Insert(r)
		if err != nil {
			t.Fatalf("insert %s: %v", r.ID, err)
		}
		applyLog(replay, ch)
	}
	got := e.AllSnapshot()
	want := batchRecompute(stream)
	if !sameViews(got, want) {
		t.Fatalf("engine view mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	// 下游按序应用全部日志后必须与引擎视图逐标识一致。
	for _, rv := range got {
		rvv, ok := replay[rv.Partition][rv.ID]
		if !ok || rvv != rv {
			t.Fatalf("replay mismatch for %s: replay=%+v view=%+v", rv.ID, rvv, rv)
		}
	}
	if got[0].HasPrev {
		t.Fatalf("first row prev must be null, got %q", got[0].Prev)
	}
	if got[1].Prev != "v10" || got[2].Prev != "v20" {
		t.Fatalf("unexpected prev chain: %+v", got)
	}
	if err := e.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 并列排序键按标识升序打破，并决定前驱归属与日志顺序。
func TestTieBreakByID(t *testing.T) {
	e := newLoggedEngine(t)
	mustInsert(t, e, Row{Partition: "p", ID: "z", SortKey: 10, Value: "Z"})
	ch, err := e.Insert(Row{Partition: "p", ID: "a", SortKey: 10, Value: "A"})
	if err != nil {
		t.Fatal(err)
	}
	// a 插入到 z 前：先输出 a（前驱为空），再修正 z（前驱变为 A）。
	if len(ch) != 2 ||
		ch[0].Kind != ChangeInsert || ch[0].ID != "a" || ch[0].HasPrev ||
		ch[1].Kind != ChangeUpdate || ch[1].ID != "z" || !ch[1].HasPrev || ch[1].Prev != "A" {
		t.Fatalf("tie insert log wrong: %+v", ch)
	}
	mustInsert(t, e, Row{Partition: "p", ID: "m", SortKey: 10, Value: "M"})
	got := e.Rows("p")
	for i, id := range []string{"a", "m", "z"} {
		if got[i].ID != id {
			t.Fatalf("pos %d id=%s want %s", i, got[i].ID, id)
		}
	}
	if got[0].HasPrev || got[1].Prev != "A" || got[2].Prev != "M" {
		t.Fatalf("tie-break prev wrong: %+v", got)
	}
	if err := e.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 插入到后继之前但新前驱值与旧前驱值相同：后继条目不输出。
func TestInsertNoOutputWhenUnchanged(t *testing.T) {
	e := newLoggedEngine(t)
	mustInsert(t, e, Row{Partition: "p", ID: "a", SortKey: 1, Value: "SAME"})
	mustInsert(t, e, Row{Partition: "p", ID: "c", SortKey: 3, Value: "C"})
	ch, err := e.Insert(Row{Partition: "p", ID: "b", SortKey: 2, Value: "SAME"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 || ch[0].Kind != ChangeInsert || ch[0].ID != "b" {
		t.Fatalf("expected only the insert entry, got %+v", ch)
	}
	got := e.Rows("p")
	if got[1].ID != "b" || !got[1].HasPrev || got[1].Prev != "SAME" || got[2].Prev != "SAME" {
		t.Fatalf("view wrong after silent insert: %+v", got)
	}
	if err := e.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 删除：先输出被删行，再输出后继修正；删除首行使后继前驱变空；删除末行无后继。
func TestDeleteFixup(t *testing.T) {
	e := newLoggedEngine(t)
	rows := []Row{
		{Partition: "p", ID: "a", SortKey: 1, Value: "A"},
		{Partition: "p", ID: "b", SortKey: 2, Value: "B"},
		{Partition: "p", ID: "c", SortKey: 3, Value: "C"},
	}
	for _, r := range rows {
		mustInsert(t, e, r)
	}
	ch, err := e.Delete("p", "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 2 ||
		ch[0].Kind != ChangeDelete || ch[0].ID != "b" ||
		ch[1].Kind != ChangeUpdate || ch[1].ID != "c" || !ch[1].HasPrev || ch[1].Prev != "A" {
		t.Fatalf("delete log wrong: %+v", ch)
	}
	if !sameViews(e.Rows("p"), batchRecompute([]Row{rows[0], rows[2]})) {
		t.Fatalf("view after delete mismatch: %+v", e.Rows("p"))
	}

	ch, _ = e.Delete("p", "a")
	if len(ch) != 2 || ch[0].ID != "a" || ch[1].ID != "c" || ch[1].HasPrev {
		t.Fatalf("expected c prev -> null, got %+v", ch)
	}

	ch, _ = e.Delete("p", "c")
	if len(ch) != 1 || ch[0].Kind != ChangeDelete {
		t.Fatalf("tail delete must emit single entry, got %+v", ch)
	}
	if _, ok := e.Snapshot("p"); ok {
		t.Fatal("partition should be removed after last row deleted")
	}
}

// 删除值与前驱相同的中间行：后继前驱值不变，只输出 delete。
func TestDeleteNoOutputWhenSameValue(t *testing.T) {
	e := newLoggedEngine(t)
	for _, r := range []Row{
		{Partition: "p", ID: "a", SortKey: 1, Value: "X"},
		{Partition: "p", ID: "b", SortKey: 2, Value: "X"},
		{Partition: "p", ID: "c", SortKey: 3, Value: "C"},
	} {
		mustInsert(t, e, r)
	}
	ch, err := e.Delete("p", "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 || ch[0].Kind != ChangeDelete || ch[0].ID != "b" {
		t.Fatalf("expected single delete (c prev stays X), got %+v", ch)
	}
	if got := e.Rows("p"); got[1].ID != "c" || got[1].Prev != "X" {
		t.Fatalf("c prev must remain X: %+v", got)
	}
	if err := e.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 不同分区互不影响；前驱为空(null) 与零值字符串 "" 严格区分。
func TestPartitionIsolationAndNullVsEmpty(t *testing.T) {
	e := newLoggedEngine(t)
	ch1, err := e.Insert(Row{Partition: "p1", ID: "a", SortKey: 1, Value: ""})
	if err != nil || ch1[0].HasPrev {
		t.Fatalf("p1 first row must be null prev: %+v %v", ch1, err)
	}
	ch2, err := e.Insert(Row{Partition: "p2", ID: "a", SortKey: 1, Value: "Y"})
	if err != nil || ch2[0].HasPrev {
		t.Fatalf("p2 first row must also be null prev: %+v %v", ch2, err)
	}
	ch3, err := e.Insert(Row{Partition: "p1", ID: "b", SortKey: 2, Value: "Z"})
	if err != nil || !ch3[0].HasPrev || ch3[0].Prev != "" {
		t.Fatalf("p1 b prev must be non-null zero-value string: %+v %v", ch3, err)
	}
	ch4, err := e.Delete("p1", "a")
	if err != nil || len(ch4) != 2 || ch4[1].ID != "b" || ch4[1].HasPrev {
		t.Fatalf("p1 b prev must become null: %+v %v", ch4, err)
	}
	if p2 := e.Rows("p2"); len(p2) != 1 || p2[0].HasPrev {
		t.Fatalf("p2 must be unaffected: %+v", p2)
	}
}

// 非法输入的拒绝原因互不相同；任何拒绝都不改变行、视图与已输出日志。
func TestRejectReasonsAndNoTrace(t *testing.T) {
	cases := []struct {
		name   string
		reason Reason
		seed   func(e *Engine)
		run    func(e *Engine) ([]Change, error)
	}{
		{"duplicate id", ReasonDuplicateID,
			func(e *Engine) { mustInsert(t, e, Row{Partition: "p", ID: "a", SortKey: 1, Value: "A"}) },
			func(e *Engine) ([]Change, error) {
				return e.Insert(Row{Partition: "p", ID: "a", SortKey: 2, Value: "X"})
			}},
		{"missing id", ReasonMissingID, nil,
			func(e *Engine) ([]Change, error) { return e.Delete("p", "ghost") }},
		{"empty partition insert", ReasonEmptyPartition, nil,
			func(e *Engine) ([]Change, error) {
				return e.Insert(Row{ID: "a", SortKey: 1, Value: "X"})
			}},
		{"empty partition delete", ReasonEmptyPartition, nil,
			func(e *Engine) ([]Change, error) { return e.Delete("", "a") }},
		{"empty id insert", ReasonEmptyID, nil,
			func(e *Engine) ([]Change, error) {
				return e.Insert(Row{Partition: "p", SortKey: 1, Value: "X"})
			}},
		{"empty id delete", ReasonEmptyID, nil,
			func(e *Engine) ([]Change, error) { return e.Delete("p", "") }},
		{"row limit exceeded", ReasonRowLimitExceeded,
			func(e *Engine) {
				mustInsert(t, e, Row{Partition: "p", ID: "a", SortKey: 1, Value: "A"})
				mustInsert(t, e, Row{Partition: "p", ID: "b", SortKey: 2, Value: "B"})
			},
			func(e *Engine) ([]Change, error) {
				return e.Insert(Row{Partition: "p", ID: "c", SortKey: 3, Value: "C"})
			}},
	}
	seen := map[Reason]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newLoggedEngine(t, WithMaxRows(2))
			if tc.seed != nil {
				tc.seed(e)
			}
			beforeView := e.AllSnapshot()
			beforeCount := e.TotalRows()

			ch, err := tc.run(e)
			var re *RejectError
			if err == nil || !errors.As(err, &re) || re.Reason != tc.reason {
				t.Fatalf("want reject reason %s, got ch=%+v err=%v", tc.reason, ch, err)
			}
			if ch != nil {
				t.Fatalf("rejected call must not emit any log entry, got %+v", ch)
			}
			seen[re.Reason] = true

			if e.TotalRows() != beforeCount || !sameViews(e.AllSnapshot(), beforeView) {
				t.Fatalf("state changed after rejection: before=%d/%+v after=%d/%+v",
					beforeCount, beforeView, e.TotalRows(), e.AllSnapshot())
			}
			if err := e.Verify(); err != nil {
				t.Fatalf("state corrupted after rejection: %v", err)
			}
		})
	}
	if len(seen) != 5 {
		t.Fatalf("reject reasons not distinct/complete: %+v", seen)
	}
}

// 同一组行以任意顺序插入，最终视图逐标识相同，且与批量重算一致。
func TestOrderIndependence(t *testing.T) {
	base := []Row{
		{Partition: "p", ID: "a", SortKey: 1, Value: "A"},
		{Partition: "p", ID: "b", SortKey: 2, Value: "B"},
		{Partition: "p", ID: "c", SortKey: 2, Value: "C"},
		{Partition: "p", ID: "d", SortKey: 3, Value: "D"},
		{Partition: "q", ID: "a", SortKey: 5, Value: "Q"},
	}
	orders := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 0, 4, 3, 1},
		{3, 1, 4, 0, 2},
	}
	var ref []RowView
	for n, ord := range orders {
		e := New()
		replay := replayView{}
		for _, i := range ord {
			ch, err := e.Insert(base[i])
			if err != nil {
				t.Fatalf("order %d: %v", n, err)
			}
			applyLog(replay, ch)
		}
		got := e.AllSnapshot()
		want := batchRecompute(base)
		if !sameViews(got, want) {
			t.Fatalf("order %d view != batch:\n got=%+v\nwant=%+v", n, got, want)
		}
		if ref == nil {
			ref = got
		} else if !sameViews(got, ref) {
			t.Fatalf("order %d view differs from order 0:\n got=%+v\n ref=%+v", n, got, ref)
		}
		// 任意顺序日志的回放结果也必须一致。
		for _, rv := range got {
			if rvv := replay[rv.Partition][rv.ID]; rvv != rv {
				t.Fatalf("order %d replay mismatch %s: %+v vs %+v", n, rv.ID, rvv, rv)
			}
		}
		if err := e.Verify(); err != nil {
			t.Fatalf("order %d verify: %v", n, err)
		}
	}
}

// 多执行体并发提交与并发自检：去重错误可接受，最终视图与批量重算一致。
func TestConcurrent(t *testing.T) {
	const perWorker = 25
	var rows []Row
	for i := 0; i < perWorker; i++ {
		rows = append(rows,
			Row{Partition: fmt.Sprintf("p%d", i%3), ID: fmt.Sprintf("id%03d", i),
				SortKey: int64(i), Value: fmt.Sprintf("v%d", i)})
	}

	e := New()
	var writers, readers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(off int) {
			defer writers.Done()
			for i := range rows {
				r := rows[(i+off)%len(rows)]
				if _, err := e.Insert(r); err != nil {
					var re *RejectError
					if !errors.As(err, &re) || re.Reason != ReasonDuplicateID {
						t.Errorf("unexpected concurrent error: %v", err)
						return
					}
				}
			}
		}(w * 7)
	}
	stop := make(chan struct{})
	for w := 0; w < 2; w++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if err := e.Verify(); err != nil {
						t.Errorf("concurrent verify: %v", err)
						return
					}
					_ = e.AllSnapshot()
					_ = e.Partitions()
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	if e.TotalRows() != len(rows) {
		t.Fatalf("row count=%d want %d", e.TotalRows(), len(rows))
	}
	if !sameViews(e.AllSnapshot(), batchRecompute(rows)) {
		t.Fatalf("concurrent final view != batch recompute: %+v", e.AllSnapshot())
	}
	if err := e.Verify(); err != nil {
		t.Fatal(err)
	}
}
