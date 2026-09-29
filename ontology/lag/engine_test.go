package lag

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// testLogger 记录每步输入、输出与判定依据，并同时打到测试日志，
// 满足“日志中打印每步输入、输出与判定依据”的要求。
type testLogger struct {
	t   *testing.T
	buf strings.Builder
}

func newTestLogger(t *testing.T) *testLogger {
	l := &testLogger{t: t}
	return l
}

func (l *testLogger) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.buf.WriteString(line)
	l.buf.WriteByte('\n')
	l.t.Log(line)
}

func (l *testLogger) String() string { return l.buf.String() }

func prevOf(v RowView) (string, bool) { return v.Prev, v.HasPrev }

func mustInsert(t *testing.T, e *Engine, r Row) []Change {
	t.Helper()
	cs, err := e.Insert(r)
	if err != nil {
		t.Fatalf("insert %q/%q unexpected error: %v", r.Partition, r.ID, err)
	}
	return cs
}

func mustDelete(t *testing.T, e *Engine, part, id string) []Change {
	t.Helper()
	cs, err := e.Delete(part, id)
	if err != nil {
		t.Fatalf("delete %q/%q unexpected error: %v", part, id, err)
	}
	return cs
}

func rejectReason(t *testing.T, err error, want Reason) {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %T: %v", err, err)
	}
	if re.Reason != want {
		t.Fatalf("want reason %s, got %s (%v)", want, re.Reason, err)
	}
}

// assertViewRecompute 校验引擎视图与对当前全部行的批量重算逐分区、逐标识一致。
func assertViewRecompute(t *testing.T, e *Engine) {
	t.Helper()
	if err := e.Verify(); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	want := Recompute(e.AllRows())
	got := e.Snapshot()
	if len(got) != len(want) {
		t.Fatalf("partition count got %d want %d", len(got), len(want))
	}
	for part, wv := range want {
		gv := got[part]
		if len(gv) != len(wv) {
			t.Fatalf("partition %q len got %d want %d", part, len(gv), len(wv))
		}
		for i := range wv {
			if gv[i] != wv[i] {
				t.Fatalf("partition %q pos %d got %+v want %+v", part, i, gv[i], wv[i])
			}
		}
	}
}

// assertMaterialized 校验下游按序应用日志后与引擎视图一致。
func assertMaterialized(t *testing.T, e *Engine, m *Materializer) {
	t.Helper()
	got, want := m.Snapshot(), e.Snapshot()
	if len(got) != len(want) {
		t.Fatalf("mat partition count got %d want %d", len(got), len(want))
	}
	for part, wv := range want {
		gv := got[part]
		if len(gv) != len(wv) {
			t.Fatalf("mat partition %q len got %d want %d", part, len(gv), len(wv))
		}
		for i := range wv {
			if gv[i] != wv[i] {
				t.Fatalf("mat partition %q pos %d got %+v want %+v", part, i, gv[i], wv[i])
			}
		}
	}
}

// TestTieBreak 覆盖排序键并列时按标识升序打破并列。
func TestTieBreak(t *testing.T) {
	lg := newTestLogger(t)
	e := New(WithLogger(lg.logf))
	m := NewMaterializer()

	// 故意乱序插入：同 sortKey=10 的 b 在 a 之前，且首行 sortKey 更大。
	for _, r := range []Row{
		{Partition: "p", ID: "b", SortKey: 10, Value: "vb"},
		{Partition: "p", ID: "a", SortKey: 10, Value: "va"},
		{Partition: "p", ID: "c", SortKey: 10, Value: "vc"},
		{Partition: "p", ID: "z", SortKey: 5, Value: "vz"},
	} {
		for _, c := range mustInsert(t, e, r) {
			m.Apply(c)
		}
	}

	view := e.View("p")
	wantOrder := []string{"z", "a", "b", "c"}
	for i, id := range wantOrder {
		if view[i].Row.ID != id {
			t.Fatalf("pos %d id got %q want %q", i, view[i].Row.ID, id)
		}
	}
	// 排序后 z(5), a(10), b(10), c(10)：z 首行空前驱，a->vz, b->va, c->vb
	if view[0].HasPrev {
		t.Fatalf("first row must have null prev, got %q", view[0].Prev)
	}
	if p, ok := prevOf(view[1]); !ok || p != "vz" {
		t.Fatalf("a prev got (%q,%v) want vz", p, ok)
	}
	if p, ok := prevOf(view[2]); !ok || p != "va" {
		t.Fatalf("b prev got (%q,%v) want va", p, ok)
	}
	if p, ok := prevOf(view[3]); !ok || p != "vb" {
		t.Fatalf("c prev got (%q,%v) want vb", p, ok)
	}
	assertViewRecompute(t, e)
	assertMaterialized(t, e, m)
}

// TestNullPrevDistinctFromZero 空前驱与零值字符串 "" 严格区分。
func TestNullPrevDistinctFromZero(t *testing.T) {
	lg := newTestLogger(t)
	e := New(WithLogger(lg.logf))
	m := NewMaterializer()

	cs1 := mustInsert(t, e, Row{Partition: "p", ID: "a", SortKey: 1, Value: ""})
	for _, c := range cs1 {
		m.Apply(c)
	}
	ins := cs1[0]
	if ins.HasPrev || ins.Prev != "" {
		t.Fatalf("first row insert: HasPrev=%v Prev=%q, want null", ins.HasPrev, ins.Prev)
	}

	cs2 := mustInsert(t, e, Row{Partition: "p", ID: "b", SortKey: 2, Value: "x"})
	for _, c := range cs2 {
		m.Apply(c)
	}
	// b 的前驱是 a，a 取值为零值字符串 ""，但 HasPrev 必须为 true。
	if !cs2[0].HasPrev || cs2[0].Prev != "" {
		t.Fatalf("b prev: HasPrev=%v Prev=%q, want HasPrev=true Prev=\"\"", cs2[0].HasPrev, cs2[0].Prev)
	}
	assertViewRecompute(t, e)
	assertMaterialized(t, e, m)
}

// TestInsertUnchangedNoUpdate 插入时若后继前驱取值未变，则不输出后继条目。
func TestInsertUnchangedNoUpdate(t *testing.T) {
	lg := newTestLogger(t)
	e := New(WithLogger(lg.logf))
	m := NewMaterializer()

	apply := func(cs []Change) {
		for _, c := range cs {
			m.Apply(c)
		}
	}

	// a(value=v), c(value=z)：c 前驱为 v。
	apply(mustInsert(t, e, Row{Partition: "p", ID: "a", SortKey: 1, Value: "v"}))
	apply(mustInsert(t, e, Row{Partition: "p", ID: "c", SortKey: 3, Value: "z"}))

	// 在 a,c 之间插入 b，取值同样为 v：c 的前驱由 a 的 v 变为 b 的 v，值不变，
	// 只应输出新行 insert 一条，且 c 不输出 update。
	cs := mustInsert(t, e, Row{Partition: "p", ID: "b", SortKey: 2, Value: "v"})
	if len(cs) != 1 || cs[0].Kind != ChangeInsert || cs[0].ID != "b" {
		t.Fatalf("got %d changes %+v, want single insert b", len(cs), cs)
	}
	if !strings.Contains(lg.String(), "no entry") {
		t.Fatalf("log should record unchanged decision, got:\n%s", lg.String())
	}
	apply(cs)
	assertViewRecompute(t, e)
	assertMaterialized(t, e, m)
}

// TestDeleteCorrection 删除后修正紧邻后继；删除首行使后继前驱变空。
func TestDeleteCorrection(t *testing.T) {
	lg := newTestLogger(t)
	e := New(WithLogger(lg.logf))
	m := NewMaterializer()
	apply := func(cs []Change) {
		for _, c := range cs {
			m.Apply(c)
		}
	}

	for _, r := range []Row{
		{Partition: "p", ID: "a", SortKey: 1, Value: "va"},
		{Partition: "p", ID: "b", SortKey: 2, Value: "vb"},
		{Partition: "p", ID: "c", SortKey: 3, Value: "vc"},
	} {
		apply(mustInsert(t, e, r))
	}

	// 删除中间行 b：先 delete b，再 update c，c 前驱由 vb 变为 va。
	cs := mustDelete(t, e, "p", "b")
	if len(cs) != 2 || cs[0].Kind != ChangeDelete || cs[0].ID != "b" ||
		cs[1].Kind != ChangeUpdate || cs[1].ID != "c" ||
		!cs[1].HasPrev || cs[1].Prev != "va" {
		t.Fatalf("delete b got %+v, want delete b then update c->va", cs)
	}
	apply(cs)
	assertViewRecompute(t, e)

	// 删除首行 a：c 前驱由 va 变为空（HasPrev false），空与零值严格区分。
	cs = mustDelete(t, e, "p", "a")
	if len(cs) != 2 || cs[0].Kind != ChangeDelete || cs[0].ID != "a" ||
		cs[1].Kind != ChangeUpdate || cs[1].ID != "c" ||
		cs[1].HasPrev || cs[1].Prev != "" {
		t.Fatalf("delete a got %+v, want delete a then update c->null", cs)
	}
	apply(cs)
	assertViewRecompute(t, e)
	assertMaterialized(t, e, m)
}

// TestDeleteUnchangedNoUpdate 删除行后后继前驱取值未变时不输出后继条目。
func TestDeleteUnchangedNoUpdate(t *testing.T) {
	lg := newTestLogger(t)
	e := New(WithLogger(lg.logf))
	m := NewMaterializer()
	apply := func(cs []Change) {
		for _, c := range cs {
			m.Apply(c)
		}
	}

	apply(mustInsert(t, e, Row{Partition: "p", ID: "a", SortKey: 1, Value: "v"}))
	apply(mustInsert(t, e, Row{Partition: "p", ID: "b", SortKey: 2, Value: "v"}))
	apply(mustInsert(t, e, Row{Partition: "p", ID: "c", SortKey: 3, Value: "z"}))

	cs := mustDelete(t, e, "p", "b")
	if len(cs) != 1 || cs[0].Kind != ChangeDelete || cs[0].ID != "b" {
		t.Fatalf("delete b got %+v, want single delete b", cs)
	}
	if !strings.Contains(lg.String(), "no entry") {
		t.Fatalf("log should record unchanged decision, got:\n%s", lg.String())
	}
	apply(cs)
	assertViewRecompute(t, e)
	assertMaterialized(t, e, m)
}

// TestPartitionIsolation 不同分区互不影响，首行前驱各自为空。
func TestPartitionIsolation(t *testing.T) {
	e := New(WithLogger(newTestLogger(t).logf))
	m := NewMaterializer()
	for _, c := range mustInsert(t, e, Row{Partition: "p1", ID: "x", SortKey: 1, Value: "1"}) {
		m.Apply(c)
	}
	for _, c := range mustInsert(t, e, Row{Partition: "p2", ID: "y", SortKey: 9, Value: "9"}) {
		m.Apply(c)
	}
	if e.View("p1")[0].HasPrev || e.View("p2")[0].HasPrev {
		t.Fatal("each partition's first row must have null prev")
	}
	assertViewRecompute(t, e)
	assertMaterialized(t, e, m)
}

// TestInvalidInputsRejectedNoTrace 各类非法输入被整体拒绝且拒绝不留痕。
func TestInvalidInputsRejectedNoTrace(t *testing.T) {
	lg := newTestLogger(t)
	e := New(WithMaxRows(2), WithLogger(lg.logf))
	m := NewMaterializer()
	for _, c := range mustInsert(t, e, Row{Partition: "p", ID: "a", SortKey: 1, Value: "v"}) {
		m.Apply(c)
	}

	snapshotBefore := fmt.Sprintf("%v", e.Snapshot())
	totalBefore := e.TotalRows()

	check := func(name string, run func() error, want Reason) {
		t.Helper()
		rejectReason(t, run(), want)
		if got := e.TotalRows(); got != totalBefore {
			t.Fatalf("%s: total rows changed %d -> %d", name, totalBefore, got)
		}
		if after := fmt.Sprintf("%v", e.Snapshot()); after != snapshotBefore {
			t.Fatalf("%s: snapshot changed:\nbefore=%s\nafter =%s", name, snapshotBefore, after)
		}
		if err := e.Verify(); err != nil {
			t.Fatalf("%s: verify after reject failed: %v", name, err)
		}
	}

	check("empty partition insert", func() error {
		_, err := e.Insert(Row{Partition: "", ID: "z", SortKey: 2, Value: "x"})
		return err
	}, ReasonEmptyPartition)

	check("empty id insert", func() error {
		_, err := e.Insert(Row{Partition: "p", ID: "", SortKey: 2, Value: "x"})
		return err
	}, ReasonEmptyID)

	check("duplicate id", func() error {
		_, err := e.Insert(Row{Partition: "p", ID: "a", SortKey: 2, Value: "other"})
		return err
	}, ReasonDuplicateID)

	for _, c := range mustInsert(t, e, Row{Partition: "p", ID: "b", SortKey: 2, Value: "w"}) {
		m.Apply(c)
	}
	snapshotBefore = fmt.Sprintf("%v", e.Snapshot())
	totalBefore = e.TotalRows()

	check("row limit", func() error {
		_, err := e.Insert(Row{Partition: "p", ID: "c", SortKey: 3, Value: "x"})
		return err
	}, ReasonRowLimitExceeded)

	check("delete missing id", func() error {
		_, err := e.Delete("p", "ghost")
		return err
	}, ReasonMissingID)

	check("delete from missing partition", func() error {
		_, err := e.Delete("nope", "a")
		return err
	}, ReasonMissingID)

	check("delete empty partition", func() error {
		_, err := e.Delete("", "a")
		return err
	}, ReasonEmptyPartition)

	check("delete empty id", func() error {
		_, err := e.Delete("p", "")
		return err
	}, ReasonEmptyID)

	assertMaterialized(t, e, m)
}

// TestReasonsDistinct 保证所有拒绝原因互不相同、可区分。
func TestReasonsDistinct(t *testing.T) {
	reasons := []Reason{
		ReasonEmptyPartition, ReasonEmptyID, ReasonDuplicateID,
		ReasonMissingID, ReasonRowLimitExceeded,
	}
	seen := map[Reason]bool{}
	for _, r := range reasons {
		if seen[r] {
			t.Fatalf("duplicate reason %q", r)
		}
		seen[r] = true
	}
}

// TestInsertOrderIndependence 同一组行以任意顺序插入，最终视图逐标识相同。
func TestInsertOrderIndependence(t *testing.T) {
	rows := []Row{
		{Partition: "p", ID: "a", SortKey: 3, Value: "A"},
		{Partition: "p", ID: "b", SortKey: 1, Value: "B"},
		{Partition: "p", ID: "c", SortKey: 1, Value: "C"},
		{Partition: "p", ID: "d", SortKey: 2, Value: "D"},
		{Partition: "q", ID: "a", SortKey: 5, Value: "QA"},
		{Partition: "q", ID: "b", SortKey: 4, Value: "QB"},
	}
	perm := rand.Perm(len(rows))

	e1, m1 := New(), NewMaterializer()
	e2, m2 := New(), NewMaterializer()
	apply := func(e *Engine, m *Materializer, r Row) {
		cs, err := e.Insert(r)
		if err != nil {
			t.Fatalf("insert %+v: %v", r, err)
		}
		for _, c := range cs {
			m.Apply(c)
		}
	}

	for _, i := range perm {
		apply(e1, m1, rows[i])
	}
	for i := len(rows) - 1; i >= 0; i-- {
		apply(e2, m2, rows[i])
	}

	want := Recompute(rows)
	for part, wv := range want {
		g1, g2 := e1.View(part), e2.View(part)
		if len(g1) != len(wv) || len(g2) != len(wv) {
			t.Fatalf("partition %q length mismatch", part)
		}
		for i := range wv {
			if g1[i] != wv[i] || g2[i] != wv[i] {
				t.Fatalf("partition %q pos %d differs from recompute:\nwant=%+v\ne1=%+v\ne2=%+v",
					part, i, wv[i], g1[i], g2[i])
			}
		}
	}
	if err := e1.Verify(); err != nil {
		t.Fatalf("e1 verify: %v", err)
	}
	if err := e2.Verify(); err != nil {
		t.Fatalf("e2 verify: %v", err)
	}
	assertMaterialized(t, e1, m1)
	assertMaterialized(t, e2, m2)
}

// TestMixedOpsMatchesRecompute 随机插入/删除序列下，引擎、日志物化视图、批量重算始终一致。
func TestMixedOpsMatchesRecompute(t *testing.T) {
	lg := newTestLogger(t)
	e := New(WithLogger(lg.logf))
	m := NewMaterializer()

	live := map[string]Row{}
	rng := rand.New(rand.NewSource(42))
	for step := 0; step < 500; step++ {
		id := fmt.Sprintf("id-%02d", rng.Intn(12))
		part := []string{"p", "q"}[rng.Intn(2)]
		key := part + "/" + id
		if _, exists := live[key]; exists {
			cs, err := e.Delete(part, id)
			if err != nil {
				t.Fatalf("step %d delete %s: %v", step, key, err)
			}
			for _, c := range cs {
				m.Apply(c)
			}
			delete(live, key)
		} else {
			r := Row{Partition: part, ID: id, SortKey: int64(rng.Intn(6)),
				Value: []string{"v0", "v1", "v0"}[rng.Intn(3)]}
			cs, err := e.Insert(r)
			if err != nil {
				t.Fatalf("step %d insert %+v: %v", step, r, err)
			}
			for _, c := range cs {
				m.Apply(c)
			}
			live[key] = r
		}
		if err := e.Verify(); err != nil {
			t.Fatalf("step %d verify: %v", step, err)
		}
		assertMaterialized(t, e, m)
	}

	all := make([]Row, 0, len(live))
	for _, r := range live {
		all = append(all, r)
	}
	if fmt.Sprintf("%v", Recompute(all)) != fmt.Sprintf("%v", e.Snapshot()) {
		t.Fatal("final snapshot differs from recompute")
	}
}

// TestConcurrentReadersAndWriters 多个执行体并发提交、读视图与自检。
func TestConcurrentReadersAndWriters(t *testing.T) {
	e := New(WithLogger(newTestLogger(t).logf))

	const writers = 8
	const perWriter = 50

	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if err := e.Verify(); err != nil {
					t.Errorf("concurrent verify: %v", err)
					return
				}
				_ = e.Snapshot()
				_ = e.View("p")
				_ = e.TotalRows()
			}
		}
	}()

	var writerWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			for i := 0; i < perWriter; i++ {
				id := fmt.Sprintf("w%d-i%02d", w, i)
				if _, err := e.Insert(Row{Partition: "p", ID: id,
					SortKey: int64(w*perWriter + i), Value: "v"}); err != nil {
					t.Errorf("concurrent insert %s: %v", id, err)
					return
				}
			}
		}(w)
	}

	writerWG.Wait()
	close(stop)
	readerWG.Wait()

	if got, want := e.TotalRows(), writers*perWriter; got != want {
		t.Fatalf("total rows got %d want %d", got, want)
	}
	if err := e.Verify(); err != nil {
		t.Fatalf("final verify: %v", err)
	}
}

// TestInsertNullToValueEmitsUpdate 插入使首行后继由空前驱变为非空，必须修正。
func TestInsertNullToValueEmitsUpdate(t *testing.T) {
	lg := newTestLogger(t)
	e := New(WithLogger(lg.logf))
	m := NewMaterializer()
	apply := func(cs []Change) {
		for _, c := range cs {
			m.Apply(c)
		}
	}

	apply(mustInsert(t, e, Row{Partition: "p", ID: "b", SortKey: 2, Value: "vb"}))
	// b 是分区首行，前驱为空。
	cs := mustInsert(t, e, Row{Partition: "p", ID: "a", SortKey: 1, Value: "va"})
	// 必须先 insert a，再 update b（空前驱 -> va）。
	if len(cs) != 2 || cs[0].Kind != ChangeInsert || cs[0].ID != "a" ||
		cs[1].Kind != ChangeUpdate || cs[1].ID != "b" ||
		!cs[1].HasPrev || cs[1].Prev != "va" {
		t.Fatalf("got %+v, want insert a then update b->va", cs)
	}
	apply(cs)
	assertViewRecompute(t, e)
	assertMaterialized(t, e, m)
}
