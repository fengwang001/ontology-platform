package subtotal

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"testing"
)

func ptr(s string) *string { return &s }
func nilp() *string        { return nil }

type stepLogger struct {
	t     *testing.T
	stepN int
}

func (l *stepLogger) input(op string, r Row) {
	l.stepN++
	l.t.Logf("步骤%02d 输入: op=%s row={id=%q dim1=%s dim2=%s amount=%d}",
		l.stepN, op, r.ID, ps(r.Dim1), ps(r.Dim2), r.Amount)
}

func (l *stepLogger) emitted(cs []Change) {
	for _, c := range cs {
		l.t.Logf("        日志: seq=%d layer=%d %s count=%d sum=%d Δcount=%d Δsum=%d "+
			"created=%v removed=%v",
			c.Seq, c.Layer, loc(c), c.Count, c.Sum, c.CDelta, c.Delta, c.Created, c.Removed)
	}
}

func (l *stepLogger) rejected(err error) {
	l.t.Logf("        判定: 整体拒绝 -> %v（状态与日志不变）", err)
}

func (l *stepLogger) basis(format string, args ...any) {
	l.t.Logf("        判定: "+format, args...)
}

func ps(p *string) string {
	if p == nil {
		return "<NULL真实空值>"
	}
	return strconv.Quote(*p)
}

func loc(c Change) string {
	switch {
	case c.Grand:
		return "key=<<TOTAL汇总占位>>"
	case c.Layer == LayerDetail:
		return fmt.Sprintf("key=(%s,%s)", ps(c.Dim1), ps(c.Dim2))
	default:
		return fmt.Sprintf("key=(%s,*)", ps(c.Dim1))
	}
}

func mustCommit(t *testing.T, l *stepLogger, s *Store, op string, r Row) []Change {
	t.Helper()
	l.input(op, r)
	cs, err := s.Commit(op, r)
	if err != nil {
		t.Fatalf("unexpected reject: %v", err)
	}
	l.emitted(cs)
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("self-check after %s %s: %v", op, r.ID, err)
	}
	l.basis("自检通过：小计/总计=明细之和，日志前缀自洽")
	return cs
}

func mustReject(t *testing.T, l *stepLogger, s *Store, op string, r Row, want RejectCode) {
	t.Helper()
	l.input(op, r)
	before := s.View()
	beforeLog := s.Log()
	cs, err := s.Commit(op, r)
	if err == nil {
		t.Fatalf("expected reject %s, got changes %+v", want, cs)
	}
	re, ok := err.(*RejectError)
	if !ok || re.Code != want {
		t.Fatalf("expected reject code %s, got %v", want, err)
	}
	l.rejected(err)
	after := s.View()
	if !EqualSnapshot(before, after) {
		t.Fatalf("state changed after rejected commit (code %s)", want)
	}
	afterLog := s.Log()
	if len(beforeLog) != len(afterLog) {
		t.Fatalf("log length changed after rejected commit (code %s)", want)
	}
	for i := range beforeLog {
		if beforeLog[i] != afterLog[i] {
			t.Fatalf("log entry %d changed after rejected commit (code %s)", i, want)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("self-check after reject %s: %v", want, err)
	}
	l.basis("拒绝原因=%s 可区分；行集/三层状态/日志不变；自检通过", want)
}

func assertBatchConsistent(t *testing.T, s *Store, tag string) {
	t.Helper()
	got := s.View()
	want := BatchRecompute(s.Rows())
	if !EqualSnapshot(got, want) {
		t.Fatalf("[%s] incremental view diverges from batch recompute:\n got=%+v\nwant=%+v",
			tag, got, want)
	}
	if replayed := Replay(s.Log()); !EqualSnapshot(replayed, got) {
		t.Fatalf("[%s] full log replay diverges from view", tag)
	}
	t.Logf("判定[%s]: 增量视图 == 日志重放 == 批量重算（总计 count=%d sum=%d, 明细组=%d, 小计组=%d）",
		tag, got.Total.Count, got.Total.Sum, len(got.Details), len(got.Sub1s))
}

// TestNullGroupingAndThreeLayers 空值作为真实取值参与分组，三层结构正确。
func TestNullGroupingAndThreeLayers(t *testing.T) {
	l := &stepLogger{t: t}
	s := New(0)

	mustCommit(t, l, s, "upsert", Row{ID: "r1", Dim1: nilp(), Dim2: nilp(), Amount: 5})
	mustCommit(t, l, s, "upsert", Row{ID: "r2", Dim1: nilp(), Dim2: ptr("b"), Amount: 7})
	mustCommit(t, l, s, "upsert", Row{ID: "r3", Dim1: ptr("a"), Dim2: nilp(), Amount: 9})

	snap := s.View()
	if got := snap.Details[Key2{ANull: true, BNull: true}]; got != (GroupStat{1, 5}) {
		t.Fatalf("null/null detail = %+v, want {1 5}", got)
	}
	if got := snap.Details[Key2{ANull: true, B: "b"}]; got != (GroupStat{1, 7}) {
		t.Fatalf("null/b detail = %+v, want {1 7}", got)
	}
	if got := snap.Sub1s[Key1{Null: true}]; got != (GroupStat{2, 12}) {
		t.Fatalf("null subtotal = %+v, want {2 12}", got)
	}
	if got := snap.Sub1s[Key1{V: "a"}]; got != (GroupStat{1, 9}) {
		t.Fatalf("a subtotal = %+v, want {1 9}", got)
	}
	if snap.Total != (GroupStat{3, 21}) {
		t.Fatalf("total = %+v, want {3 21}", snap.Total)
	}
	l.basis("NULL 是真实键，NULL/NULL 与 NULL/\"b\" 为不同明细组，同属 NULL 小计组")

	for _, c := range s.Log() {
		if c.Layer == LayerDetail && c.CDelta == 1 && !c.Created {
			t.Fatalf("positive insert change missing Created: %+v", c)
		}
		if c.Grand && (c.Dim1 != nil || c.Dim2 != nil) {
			t.Fatalf("grand total change must not carry dims: %+v", c)
		}
	}
	l.basis("新建变更 created=true；总计层 grand=true 且无维度，与空值维度严格区分")
	assertBatchConsistent(t, s, "null-grouping")
}

// TestZeroSumGroupKept 计数>0 但求和为 0 的组必须保留。
func TestZeroSumGroupKept(t *testing.T) {
	l := &stepLogger{t: t}
	s := New(0)

	mustCommit(t, l, s, "upsert", Row{ID: "p", Dim1: ptr("x"), Dim2: ptr("y"), Amount: 10})
	mustCommit(t, l, s, "upsert", Row{ID: "q", Dim1: ptr("x"), Dim2: ptr("y"), Amount: -10})

	snap := s.View()
	g, ok := snap.Details[Key2{A: "x", B: "y"}]
	if !ok || g != (GroupStat{2, 0}) {
		t.Fatalf("zero-sum detail = (present=%v, %+v), want present {2 0}", ok, g)
	}
	if sg := snap.Sub1s[Key1{V: "x"}]; sg != (GroupStat{2, 0}) {
		t.Fatalf("zero-sum subtotal = %+v, want {2 0}", sg)
	}
	if snap.Total != (GroupStat{2, 0}) {
		t.Fatalf("zero-sum total = %+v, want {2 0}", snap.Total)
	}
	l.basis("count=2>0 且 sum=0：明细组、小计组均保留，不按和为零删除")
	assertBatchConsistent(t, s, "zero-sum-kept")
}

// TestWithdrawalAndGroupRemoval 撤回现存行、组归零删除、removed 标记。
func TestWithdrawalAndGroupRemoval(t *testing.T) {
	l := &stepLogger{t: t}
	s := New(0)

	mustCommit(t, l, s, "upsert", Row{ID: "a1", Dim1: ptr("x"), Dim2: ptr("y"), Amount: 4})
	mustCommit(t, l, s, "upsert", Row{ID: "a2", Dim1: ptr("x"), Dim2: ptr("z"), Amount: 6})
	cs := mustCommit(t, l, s, "delete", Row{ID: "a1"})

	if _, ok := s.View().Details[Key2{A: "x", B: "y"}]; ok {
		t.Fatal("detail group should be removed when count hits zero")
	}
	var removedDetail bool
	for _, c := range cs {
		if c.Layer == LayerDetail && c.Removed && c.CDelta == -1 {
			removedDetail = true
		}
	}
	if !removedDetail {
		t.Fatalf("withdrawal did not emit removed detail change: %+v", cs)
	}
	snap := s.View()
	if sg := snap.Sub1s[Key1{V: "x"}]; sg != (GroupStat{1, 6}) {
		t.Fatalf("subtotal after withdraw = %+v, want {1 6}", sg)
	}
	if snap.Total != (GroupStat{1, 6}) {
		t.Fatalf("total after withdraw = %+v, want {1 6}", snap.Total)
	}
	l.basis("撤回现存行：归零明细组删除且仅输出负向 removed；小计/总计收缩")

	mustCommit(t, l, s, "delete", Row{ID: "a2"})
	snap = s.View()
	if len(snap.Details) != 0 || len(snap.Sub1s) != 0 {
		t.Fatalf("after all withdrawals, detail/subtotal maps must be empty: %+v", snap)
	}
	if snap.Total != (GroupStat{0, 0}) {
		t.Fatalf("empty total = %+v, want retained placeholder {0 0}", snap.Total)
	}
	l.basis("全部撤回：明细/小计映射为空，总计占位恒保留为 {0 0}")
	assertBatchConsistent(t, s, "withdraw-all")
}

// TestUpsertMovesGroup 覆盖写导致行在组间迁移，及幂等重复插入。
func TestUpsertMovesGroup(t *testing.T) {
	l := &stepLogger{t: t}
	s := New(0)

	mustCommit(t, l, s, "upsert", Row{ID: "m", Dim1: ptr("x"), Dim2: ptr("y"), Amount: 3})
	mustCommit(t, l, s, "upsert", Row{ID: "m", Dim1: ptr("x"), Dim2: ptr("z"), Amount: 5})

	snap := s.View()
	if _, ok := snap.Details[Key2{A: "x", B: "y"}]; ok {
		t.Fatal("old detail group of moved row should be gone")
	}
	if g := snap.Details[Key2{A: "x", B: "z"}]; g != (GroupStat{1, 5}) {
		t.Fatalf("new detail group = %+v, want {1 5}", g)
	}
	if g := snap.Sub1s[Key1{V: "x"}]; g != (GroupStat{1, 5}) {
		t.Fatalf("subtotal = %+v, want {1 5}", g)
	}
	l.basis("覆盖写=原子先撤回旧键再加入新键；每层日志先负后正，前缀仍自洽")

	beforeN := len(s.Log())
	cs := mustCommit(t, l, s, "upsert", Row{ID: "m", Dim1: ptr("x"), Dim2: ptr("z"), Amount: 5})
	if len(cs) != 0 || len(s.Log()) != beforeN {
		t.Fatalf("identical upsert must be a no-op, cs=%+v", cs)
	}
	l.basis("内容相同的重复 upsert 无变更、无日志（幂等）")
	assertBatchConsistent(t, s, "upsert-move")
}

// TestRejectCategories 四类互不相同、可区分的非法输入，拒绝后零痕迹。
func TestRejectCategories(t *testing.T) {
	s := New(2)
	l := &stepLogger{t: t}

	mustCommit(t, l, s, "upsert", Row{ID: "g1", Dim1: ptr("a"), Dim2: ptr("1"), Amount: 1})
	mustCommit(t, l, s, "upsert", Row{ID: "g2", Dim1: ptr("b"), Dim2: ptr("2"), Amount: 1})

	codes := map[RejectCode]bool{}

	mustReject(t, l, s, "frobnicate", Row{ID: "x", Amount: 1}, RejectMalformed)
	mustReject(t, l, s, "upsert", Row{ID: "", Amount: 1}, RejectMalformed)
	codes[RejectMalformed] = true

	mustReject(t, l, s, "delete", Row{ID: "ghost"}, RejectRowIDNotFound)
	codes[RejectRowIDNotFound] = true

	mustReject(t, l, s, "upsert",
		Row{ID: "g3", Dim1: ptr("c"), Dim2: ptr("3"), Amount: 1}, RejectDetailLimit)
	codes[RejectDetailLimit] = true

	// 覆盖写入已有明细组不触发上限，证明限额按“组数”而非“行数”。
	mustCommit(t, l, s, "upsert", Row{ID: "g2", Dim1: ptr("b"), Dim2: ptr("2"), Amount: 8})

	big := New(0)
	bl := &stepLogger{t: t}
	mustCommit(t, bl, big, "upsert",
		Row{ID: "big", Dim1: ptr("a"), Dim2: ptr("a"), Amount: math.MaxInt64})
	mustReject(t, bl, big, "upsert",
		Row{ID: "big2", Dim1: ptr("a"), Dim2: ptr("a"), Amount: 1}, RejectAmountInvalid)
	codes[RejectAmountInvalid] = true

	if len(codes) != 4 {
		t.Fatalf("expected 4 distinct reject categories, got %d: %v", len(codes), codes)
	}
	assertBatchConsistent(t, s, "after-rejects")
	assertBatchConsistent(t, big, "overflow-rejected")
	l.basis("四类错误码互不相同：%v；每次拒绝前后快照与日志逐条相等", codes)
}

// TestPrefixLogSelfConsistent 每个整条增量前缀重放后三层自洽。
func TestPrefixLogSelfConsistent(t *testing.T) {
	s := New(0)
	l := &stepLogger{t: t}
	steps := []struct {
		op  string
		row Row
	}{
		{"upsert", Row{ID: "1", Dim1: ptr("a"), Dim2: ptr("x"), Amount: 2}},
		{"upsert", Row{ID: "2", Dim1: ptr("a"), Dim2: ptr("y"), Amount: -2}},
		{"upsert", Row{ID: "3", Dim1: nilp(), Dim2: nilp(), Amount: 5}},
		{"upsert", Row{ID: "1", Dim1: ptr("b"), Dim2: ptr("x"), Amount: 7}},
		{"delete", Row{ID: "2"}},
		{"delete", Row{ID: "3"}},
		{"delete", Row{ID: "1"}},
	}
	for i, st := range steps {
		mustCommit(t, l, s, st.op, st.row)
		full := s.Log()
		boundaries := 0
		for j := range full {
			if !isCommitBoundary(full, j) {
				continue
			}
			boundaries++
			if err := consistent(Replay(full[:j+1])); err != nil {
				t.Fatalf("prefix after step %d inconsistent: %v", i+1, err)
			}
		}
		if boundaries != i+1 {
			t.Fatalf("after step %d found %d commit boundaries, want %d", i+1, boundaries, i+1)
		}
	}
	assertBatchConsistent(t, s, "prefixes")
	l.basis("每个已提交增量边界的日志前缀重放均三层自洽，边界数=已提交增量数")
}

// TestConcurrentCommitViewCheck 多执行体并发提交/撤回/视图/自检，配合 -race。
func TestConcurrentCommitViewCheck(t *testing.T) {
	const writers = 8
	const perWriter = 60
	s := New(0)

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			d := strconv.Itoa(w % 3)
			for i := 0; i < perWriter; i++ {
				id := fmt.Sprintf("w%d-%d", w, i)
				if _, err := s.Commit("upsert", Row{
					ID:     id,
					Dim1:   ptr(d),
					Dim2:   ptr(strconv.Itoa(i % 4)),
					Amount: int64(i - 30),
				}); err != nil {
					t.Errorf("commit %s: %v", id, err)
					return
				}
				if i%7 == 0 {
					if _, err := s.Commit("delete", Row{ID: id}); err != nil {
						t.Errorf("delete %s: %v", id, err)
						return
					}
				}
			}
		}(w)
	}

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				if err := s.SelfCheck(); err != nil {
					t.Errorf("concurrent self-check: %v", err)
					return
				}
				_ = s.View()
				_ = s.Log()
			}
		}()
	}

	wg.Wait()
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("final self-check: %v", err)
	}
	assertBatchConsistent(t, s, "concurrent")
}
