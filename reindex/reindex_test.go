package reindex

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/dest"
	"ontology/source"
)

func newSys(t *testing.T, limit int) (*source.Source, *dest.Dest, *Coordinator) {
	t.Helper()
	d, err := dest.New(limit)
	if err != nil {
		t.Fatal(err)
	}
	src := source.New()
	return src, d, New(src, d)
}

func mustPut(t *testing.T, s *source.Source, id, body string) uint64 {
	t.Helper()
	seq, err := s.Put(id, []byte(body))
	if err != nil {
		t.Fatalf("Put(%q): %v", id, err)
	}
	return seq
}

func mustDel(t *testing.T, s *source.Source, id string) uint64 {
	t.Helper()
	seq, err := s.Delete(id)
	if err != nil {
		t.Fatalf("Delete(%q): %v", id, err)
	}
	return seq
}

// step 调用 Step 并断言无错误。
func step(t *testing.T, c *Coordinator) (applied, conflicts, incompatible int, done bool) {
	t.Helper()
	a, cf, inc, done, err := c.Step()
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	return a, cf, inc, done
}

// checkCutoverInvariant 验证切换不变量：dest 存活集 == 源存活集 − 失败集，
// 每条 body 相同、ver 等于源中的 seq，且无任何墓碑残留。
func checkCutoverInvariant(t *testing.T, src *source.Source, d *dest.Dest, failures map[string]bool, ctx string) {
	t.Helper()
	all := d.All()
	for id, rec := range all {
		if rec.Tombstone {
			t.Errorf("%s: 切换后 dest 仍有墓碑 %q", ctx, id)
		}
	}
	wantLive := map[string]source.Doc{}
	for _, doc := range src.LiveDocs() {
		if !failures[doc.ID] {
			wantLive[doc.ID] = doc
		}
	}
	if len(all) != len(wantLive) {
		t.Errorf("%s: dest 存活 %d 条, 期望 %d 条", ctx, len(all), len(wantLive))
	}
	for id, doc := range wantLive {
		rec, ok := all[id]
		if !ok {
			t.Errorf("%s: %q 在 dest 缺失", ctx, id)
			continue
		}
		if rec.Ver != doc.Seq || string(rec.Body) != string(doc.Body) {
			t.Errorf("%s: %q 实得 ver=%d body=%q, 期望 ver=%d body=%q",
				ctx, id, rec.Ver, rec.Body, doc.Seq, doc.Body)
		}
	}
}

// TestExampleFromSpec 是题目示例（L=4, B=2）：回填落后于在途双写。
func TestExampleFromSpec(t *testing.T) {
	src, d, c := newSys(t, 4)
	mustPut(t, src, "a", "1") // seq 1
	mustPut(t, src, "b", "2") // seq 2
	mustPut(t, src, "c", "3") // seq 3
	if err := c.Start(2); err != nil {
		t.Fatal(err)
	}
	mustDel(t, src, "b")      // seq 4：dest 对无记录的 b 留墓碑 4
	mustPut(t, src, "a", "9") // seq 5：dest 有 a@5
	a, cf, inc, done := step(t, c)
	if a != 0 || cf != 2 || inc != 0 || done {
		t.Fatalf("第一次 Step = (%d,%d,%d,%v), want (0,2,0,false)", a, cf, inc, done)
	}
	a, cf, inc, done = step(t, c)
	if a != 1 || cf != 0 || inc != 0 || !done {
		t.Fatalf("第二次 Step = (%d,%d,%d,%v), want (1,0,0,true)", a, cf, inc, done)
	}
	if err := c.Cutover(0); err != nil {
		t.Fatalf("Cutover(0): %v", err)
	}
	// dest 应为 a@5、c@3；b 的墓碑被清除，不会被回填复活。
	want := map[string]dest.Record{
		"a": {Ver: 5, Body: []byte("9")},
		"c": {Ver: 3, Body: []byte("3")},
	}
	if got := d.All(); !reflect.DeepEqual(got, want) {
		t.Fatalf("切换后 dest = %+v, want %+v", got, want)
	}
	if got := d.Conflicts(); got != 2 {
		t.Fatalf("累计冲突 = %d, want 2", got)
	}
	if c.State() != Switched {
		t.Fatalf("状态 = %v, want Switched", c.State())
	}
	// 切换后源写入报已切换。
	if _, err := src.Put("x", []byte("1")); !errors.Is(err, source.ErrSwitched) {
		t.Fatalf("切换后 Put: %v, want ErrSwitched", err)
	}
	if _, err := src.Delete("a"); !errors.Is(err, source.ErrSwitched) {
		t.Fatalf("切换后 Delete: %v, want ErrSwitched", err)
	}
	checkCutoverInvariant(t, src, d, nil, "题目示例")
}

// TestStartRejections 覆盖 Start 的拒绝次序：参数非法 > 状态不符（含 dest 非空）。
func TestStartRejections(t *testing.T) {
	t.Run("B 越界", func(t *testing.T) {
		for _, B := range []int{-1, 0, 1001, 1 << 20} {
			_, _, c := newSys(t, 4)
			if err := c.Start(B); !errors.Is(err, ErrInvalidParam) {
				t.Errorf("Start(%d): %v, want ErrInvalidParam", B, err)
			}
		}
	})
	t.Run("dest 非空属状态不符", func(t *testing.T) {
		_, d, c := newSys(t, 4)
		d.Index("x", nil, 1)
		if err := c.Start(1); !errors.Is(err, ErrBadState) {
			t.Fatalf("Start: %v, want ErrBadState", err)
		}
	})
	t.Run("非 Idle", func(t *testing.T) {
		_, _, c := newSys(t, 4)
		if err := c.Start(1); err != nil {
			t.Fatal(err)
		}
		if err := c.Start(1); !errors.Is(err, ErrBadState) {
			t.Fatalf("重复 Start: %v, want ErrBadState", err)
		}
	})
	t.Run("参数非法优先于状态不符", func(t *testing.T) {
		_, _, c := newSys(t, 4)
		if err := c.Start(1); err != nil {
			t.Fatal(err)
		}
		if err := c.Start(0); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Running 中 Start(0): %v, want ErrInvalidParam", err)
		}
	})
}

// TestCutoverRejectionOrder 覆盖 Cutover 的拒绝次序：
// 参数非法 > 状态不是 Running > 回填未完成 > 失败集合大小严格大于 tol。
func TestCutoverRejectionOrder(t *testing.T) {
	t.Run("参数非法最优先", func(t *testing.T) {
		_, _, c := newSys(t, 4) // Idle 状态
		for _, tol := range []int{-1, 1000001, 1 << 30} {
			if err := c.Cutover(tol); !errors.Is(err, ErrInvalidParam) {
				t.Errorf("Idle 中 Cutover(%d): %v, want ErrInvalidParam", tol, err)
			}
		}
	})
	t.Run("状态不是 Running", func(t *testing.T) {
		_, _, c := newSys(t, 4)
		if err := c.Cutover(0); !errors.Is(err, ErrBadState) {
			t.Fatalf("Idle 中 Cutover: %v, want ErrBadState", err)
		}
		// Switched 状态同样拒绝。
		if err := c.Start(1); err != nil {
			t.Fatal(err)
		}
		if err := c.Cutover(0); err != nil { // 空快照，直接可切换
			t.Fatal(err)
		}
		if err := c.Cutover(0); !errors.Is(err, ErrBadState) {
			t.Fatalf("Switched 中 Cutover: %v, want ErrBadState", err)
		}
	})
	t.Run("回填未完成", func(t *testing.T) {
		src, _, c := newSys(t, 4)
		mustPut(t, src, "a", "1")
		if err := c.Start(1); err != nil {
			t.Fatal(err)
		}
		if err := c.Cutover(0); !errors.Is(err, ErrBackfillPending) {
			t.Fatalf("Cutover: %v, want ErrBackfillPending", err)
		}
	})
	t.Run("tol 恰等通过与小 1 拒绝", func(t *testing.T) {
		build := func(t *testing.T, nBad int) *Coordinator {
			src, _, c := newSys(t, 4)
			for i := 0; i < nBad; i++ {
				mustPut(t, src, fmt.Sprintf("bad%d", i), "toolong")
			}
			if err := c.Start(1000); err != nil {
				t.Fatal(err)
			}
			step(t, c) // 回填完，全部写成不兼容墓碑
			return c
		}
		c := build(t, 2)
		if err := c.Cutover(1); !errors.Is(err, ErrTooManyFailures) {
			t.Fatalf("失败 2 个 Cutover(1): %v, want ErrTooManyFailures", err)
		}
		if err := c.Cutover(2); err != nil {
			t.Fatalf("失败 2 个 Cutover(2): %v, want nil", err)
		}
		c = build(t, 1)
		if err := c.Cutover(0); !errors.Is(err, ErrTooManyFailures) {
			t.Fatalf("失败 1 个 Cutover(0): %v, want ErrTooManyFailures", err)
		}
		if err := c.Cutover(1); err != nil {
			t.Fatalf("失败 1 个 Cutover(1): %v, want nil", err)
		}
	})
}

// TestStepAbortRejections 覆盖 Step/Abort 的状态拒绝与"被拒不改状态"。
func TestStepAbortRejections(t *testing.T) {
	_, d, c := newSys(t, 4)
	if _, _, _, _, err := c.Step(); !errors.Is(err, ErrBadState) {
		t.Fatalf("Idle 中 Step: %v, want ErrBadState", err)
	}
	if err := c.Abort(); !errors.Is(err, ErrBadState) {
		t.Fatalf("Idle 中 Abort: %v, want ErrBadState", err)
	}
	if c.State() != Idle || !d.Empty() || d.Conflicts() != 0 {
		t.Fatal("被拒操作改变了状态")
	}
}

// TestStepAfterDone 回填完之后再 Step 返回全 0 与真，不改状态。
func TestStepAfterDone(t *testing.T) {
	src, _, c := newSys(t, 4)
	mustPut(t, src, "a", "1")
	if err := c.Start(1); err != nil {
		t.Fatal(err)
	}
	step(t, c)
	for i := 0; i < 3; i++ {
		a, cf, inc, done := step(t, c)
		if a != 0 || cf != 0 || inc != 0 || !done {
			t.Fatalf("完成后 Step = (%d,%d,%d,%v), want (0,0,0,true)", a, cf, inc, done)
		}
	}
	if c.State() != Running {
		t.Fatalf("状态 = %v, want Running", c.State())
	}
}

// TestAbortRestart 覆盖 Abort 清空 dest 回到 Idle 并可重新 Start。
func TestAbortRestart(t *testing.T) {
	src, d, c := newSys(t, 4)
	mustPut(t, src, "a", "1") // seq 1
	mustPut(t, src, "b", "2") // seq 2
	if err := c.Start(1); err != nil {
		t.Fatal(err)
	}
	mustPut(t, src, "a", "9") // seq 3：双写 a@3
	step(t, c)                // 回填 a@1 → 冲突
	if d.Conflicts() != 1 {
		t.Fatalf("冲突数 = %d, want 1", d.Conflicts())
	}
	if err := c.Abort(); err != nil {
		t.Fatal(err)
	}
	if c.State() != Idle {
		t.Fatalf("状态 = %v, want Idle", c.State())
	}
	if !d.Empty() || d.Conflicts() != 0 {
		t.Fatal("Abort 未清空 dest")
	}
	// 源未切换，仍可写。
	mustPut(t, src, "c", "3") // seq 4
	// 重新 Start：快照为 a@3、b@2、c@4。
	if err := c.Start(2); err != nil {
		t.Fatal(err)
	}
	step(t, c)
	_, _, _, done := step(t, c)
	if !done {
		t.Fatal("回填应已完成")
	}
	if err := c.Cutover(0); err != nil {
		t.Fatal(err)
	}
	checkCutoverInvariant(t, src, d, nil, "Abort 后重启")
}

// TestRejectedOpsKeepCounters 验证各类被拒操作不改任何状态与计数。
func TestRejectedOpsKeepCounters(t *testing.T) {
	src, d, c := newSys(t, 4)
	mustPut(t, src, "a", "1") // seq 1
	if err := c.Start(2); err != nil {
		t.Fatal(err)
	}
	mustPut(t, src, "a", "22") // seq 2：双写应用
	before := d.Conflicts()
	rejects := []struct {
		name string
		op   func() error
		want error
	}{
		{"Start 参数非法", func() error { return c.Start(0) }, ErrInvalidParam},
		{"Start 状态不符", func() error { return c.Start(2) }, ErrBadState},
		{"Cutover 参数非法", func() error { return c.Cutover(-1) }, ErrInvalidParam},
		{"Cutover 回填未完", func() error { return c.Cutover(0) }, ErrBackfillPending},
		{"源 Delete 文档不存在", func() error { _, e := src.Delete("missing"); return e }, source.ErrDocNotFound},
		{"源 Put 参数非法", func() error { _, e := src.Put("", nil); return e }, source.ErrInvalidID},
	}
	for _, r := range rejects {
		if err := r.op(); !errors.Is(err, r.want) {
			t.Errorf("%s: %v, want %v", r.name, err, r.want)
		}
	}
	if got := d.Conflicts(); got != before {
		t.Fatalf("被拒操作改变冲突计数: %d → %d", before, got)
	}
	if c.State() != Running {
		t.Fatalf("被拒操作改变状态: %v", c.State())
	}
	// 被拒的源写入不占号：下一个 Put 应得 seq 3。
	if seq := mustPut(t, src, "b", "3"); seq != 3 {
		t.Fatalf("seq = %d, want 3（被拒操作不应占号）", seq)
	}
}

// TestScanned 证明单次 Step 读取的快照条目数恰为 min(B, 剩余)，与总量无关。
func TestScanned(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		t.Run(fmt.Sprintf("快照%d条", n), func(t *testing.T) {
			src, _, c := newSys(t, 16)
			for i := 0; i < n; i++ {
				mustPut(t, src, fmt.Sprintf("id%06d", i), "v")
			}
			if err := c.Start(3); err != nil {
				t.Fatal(err)
			}
			step(t, c)
			if c.scanned != 3 {
				t.Fatalf("单次 Step 后 scanned = %d, want 3（与总量 %d 无关）", c.scanned, n)
			}
		})
	}
	t.Run("末批恰为剩余条数", func(t *testing.T) {
		src, _, c := newSys(t, 16)
		for i := 0; i < 4; i++ {
			mustPut(t, src, fmt.Sprintf("id%d", i), "v")
		}
		if err := c.Start(3); err != nil {
			t.Fatal(err)
		}
		step(t, c) // 读 3 条
		if c.scanned != 3 {
			t.Fatalf("scanned = %d, want 3", c.scanned)
		}
		_, _, _, done := step(t, c) // 读 min(3, 1) = 1 条
		if c.scanned != 4 || !done {
			t.Fatalf("scanned = %d, done = %v, want 4, true", c.scanned, done)
		}
		step(t, c) // 读 0 条
		if c.scanned != 4 {
			t.Fatalf("完成后 scanned = %d, want 4", c.scanned)
		}
	})
}

// TestBackfillAheadOfDoubleWrite 是回填领先于双写的交错。
func TestBackfillAheadOfDoubleWrite(t *testing.T) {
	src, d, c := newSys(t, 4)
	mustPut(t, src, "a", "1") // seq 1
	mustPut(t, src, "b", "2") // seq 2
	if err := c.Start(2); err != nil {
		t.Fatal(err)
	}
	// 先回填完，再发生双写：双写版本更大，干净应用。
	a, cf, inc, done := step(t, c)
	if a != 2 || cf != 0 || inc != 0 || !done {
		t.Fatalf("Step = (%d,%d,%d,%v), want (2,0,0,true)", a, cf, inc, done)
	}
	mustPut(t, src, "a", "9") // seq 3：覆盖 a@1
	mustDel(t, src, "b")      // seq 4：墓碑 b@4
	if err := c.Cutover(0); err != nil {
		t.Fatal(err)
	}
	want := map[string]dest.Record{
		"a": {Ver: 3, Body: []byte("9")},
	}
	if got := d.All(); !reflect.DeepEqual(got, want) {
		t.Fatalf("切换后 dest = %+v, want %+v", got, want)
	}
	checkCutoverInvariant(t, src, d, nil, "回填领先")
}

// TestIncompatibleExample 是题目第二个示例：不兼容写入、失败集合与容忍度。
func TestIncompatibleExample(t *testing.T) {
	setup := func(t *testing.T) (*source.Source, *dest.Dest, *Coordinator) {
		src, d, c := newSys(t, 4)
		mustPut(t, src, "a", "1") // seq 1
		mustPut(t, src, "b", "2") // seq 2
		mustPut(t, src, "c", "3") // seq 3
		if err := c.Start(2); err != nil {
			t.Fatal(err)
		}
		mustDel(t, src, "b")      // seq 4
		mustPut(t, src, "a", "9") // seq 5
		step(t, c)
		step(t, c) // 回填完成
		return src, d, c
	}
	t.Run("Cutover(0) 拒绝而 Cutover(1) 通过", func(t *testing.T) {
		src, d, c := setup(t)
		mustPut(t, src, "c", "toolong") // seq 6：c 变为不兼容墓碑 6
		if got := d.Failures(); !reflect.DeepEqual(got, map[string]bool{"c": true}) {
			t.Fatalf("失败集合 = %v, want {c}", got)
		}
		if err := c.Cutover(0); !errors.Is(err, ErrTooManyFailures) {
			t.Fatalf("Cutover(0): %v, want ErrTooManyFailures", err)
		}
		if err := c.Cutover(1); err != nil {
			t.Fatalf("Cutover(1): %v", err)
		}
		want := map[string]dest.Record{"a": {Ver: 5, Body: []byte("9")}}
		if got := d.All(); !reflect.DeepEqual(got, want) {
			t.Fatalf("切换后 dest = %+v, want %+v", got, want)
		}
		checkCutoverInvariant(t, src, d, map[string]bool{"c": true}, "不兼容示例")
	})
	t.Run("更大版本写入使失败集合自动移出", func(t *testing.T) {
		src, d, c := setup(t)
		mustPut(t, src, "c", "toolong") // seq 6：不兼容墓碑
		mustPut(t, src, "c", "ok")      // seq 7：c@7 应用，失败集合变空
		if n := len(d.Failures()); n != 0 {
			t.Fatalf("失败集合大小 = %d, want 0", n)
		}
		if err := c.Cutover(0); err != nil {
			t.Fatalf("Cutover(0): %v", err)
		}
		want := map[string]dest.Record{
			"a": {Ver: 5, Body: []byte("9")},
			"c": {Ver: 7, Body: []byte("ok")},
		}
		if got := d.All(); !reflect.DeepEqual(got, want) {
			t.Fatalf("切换后 dest = %+v, want %+v", got, want)
		}
		checkCutoverInvariant(t, src, d, nil, "失败集合移出")
	})
	t.Run("快照中的不兼容文档在回填时处理", func(t *testing.T) {
		src, d, c := newSys(t, 4)
		mustPut(t, src, "a", "toolong") // seq 1：快照里 body 超 L
		mustPut(t, src, "b", "2")       // seq 2
		if err := c.Start(2); err != nil {
			t.Fatal(err)
		}
		// 未被更新版本抢先：写成不兼容墓碑并计入不兼容数。
		a, cf, inc, done := step(t, c)
		if a != 1 || cf != 0 || inc != 1 || !done {
			t.Fatalf("Step = (%d,%d,%d,%v), want (1,0,1,true)", a, cf, inc, done)
		}
		if got := d.Failures(); !reflect.DeepEqual(got, map[string]bool{"a": true}) {
			t.Fatalf("失败集合 = %v, want {a}", got)
		}
		// 变体：被更新版本抢先则只计冲突。新起一轮验证。
		src2, d2, c2 := newSys(t, 4)
		mustPut(t, src2, "a", "toolong") // seq 1
		if err := c2.Start(1); err != nil {
			t.Fatal(err)
		}
		mustPut(t, src2, "a", "ok") // seq 2：双写抢先
		a, cf, inc, done = step(t, c2)
		if a != 0 || cf != 1 || inc != 0 || !done {
			t.Fatalf("Step = (%d,%d,%d,%v), want (0,1,0,true)", a, cf, inc, done)
		}
		if n := len(d2.Failures()); n != 0 {
			t.Fatalf("失败集合大小 = %d, want 0", n)
		}
	})
}

// ---------------------------------------------------------------------------
// 朴素模拟：独立实现同一套语义，用于逐步对拍。
// ---------------------------------------------------------------------------

type mrec struct {
	ver      uint64
	body     string
	tomb     bool
	incompat bool
}

type model struct {
	limit     int
	seq       uint64
	docs      map[string]source.Doc
	switched  bool
	recs      map[string]mrec
	conflicts uint64
	state     State
	snapshot  []source.Doc
	scanned   int
	batch     int
}

func newModel(limit int) *model {
	return &model{limit: limit, docs: map[string]source.Doc{}, recs: map[string]mrec{}}
}

func (m *model) dIndex(id, body string, ver uint64) (bool, bool) {
	if rec, ok := m.recs[id]; ok && ver <= rec.ver {
		m.conflicts++
		return false, false
	}
	if len(body) > m.limit {
		m.recs[id] = mrec{ver: ver, tomb: true, incompat: true}
		return true, true
	}
	m.recs[id] = mrec{ver: ver, body: body}
	return true, false
}

func (m *model) dDelete(id string, ver uint64) {
	if rec, ok := m.recs[id]; ok && ver <= rec.ver {
		m.conflicts++
		return
	}
	m.recs[id] = mrec{ver: ver, tomb: true}
}

func (m *model) put(id, body string) (uint64, error) {
	if len(id) < 1 || len(id) > source.MaxIDLen {
		return 0, source.ErrInvalidID
	}
	if len(body) > source.MaxBodyLen {
		return 0, source.ErrInvalidBody
	}
	if m.switched {
		return 0, source.ErrSwitched
	}
	m.seq++
	m.docs[id] = source.Doc{ID: id, Body: []byte(body), Seq: m.seq}
	if m.state == Running {
		m.dIndex(id, body, m.seq)
	}
	return m.seq, nil
}

func (m *model) del(id string) (uint64, error) {
	if len(id) < 1 || len(id) > source.MaxIDLen {
		return 0, source.ErrInvalidID
	}
	if m.switched {
		return 0, source.ErrSwitched
	}
	if _, ok := m.docs[id]; !ok {
		return 0, source.ErrDocNotFound
	}
	m.seq++
	delete(m.docs, id)
	if m.state == Running {
		m.dDelete(id, m.seq)
	}
	return m.seq, nil
}

func (m *model) start(B int) error {
	if B < MinBatch || B > MaxBatch {
		return ErrInvalidParam
	}
	if m.state != Idle || len(m.recs) != 0 {
		return ErrBadState
	}
	m.snapshot = m.snapshot[:0]
	for _, doc := range m.docs {
		m.snapshot = append(m.snapshot, doc)
	}
	sort.Slice(m.snapshot, func(i, j int) bool { return m.snapshot[i].ID < m.snapshot[j].ID })
	m.batch = B
	m.scanned = 0
	m.state = Running
	return nil
}

func (m *model) step() (int, int, int, bool, error) {
	if m.state != Running {
		return 0, 0, 0, false, ErrBadState
	}
	rest := len(m.snapshot) - m.scanned
	n := m.batch
	if rest < n {
		n = rest
	}
	var a, cf, inc int
	for i := 0; i < n; i++ {
		doc := m.snapshot[m.scanned+i]
		ap, ic := m.dIndex(doc.ID, string(doc.Body), doc.Seq)
		switch {
		case ic:
			inc++
		case ap:
			a++
		default:
			cf++
		}
	}
	m.scanned += n
	return a, cf, inc, m.scanned == len(m.snapshot), nil
}

func (m *model) failures() map[string]bool {
	out := map[string]bool{}
	for id, r := range m.recs {
		if r.tomb && r.incompat {
			out[id] = true
		}
	}
	return out
}

func (m *model) cutover(tol int) error {
	if tol < 0 || tol > MaxTol {
		return ErrInvalidParam
	}
	if m.state != Running {
		return ErrBadState
	}
	if m.scanned < len(m.snapshot) {
		return ErrBackfillPending
	}
	if len(m.failures()) > tol {
		return ErrTooManyFailures
	}
	for id, r := range m.recs {
		if r.tomb {
			delete(m.recs, id)
		}
	}
	m.switched = true
	m.snapshot = nil
	m.state = Switched
	return nil
}

func (m *model) abort() error {
	if m.state != Running {
		return ErrBadState
	}
	m.recs = map[string]mrec{}
	m.conflicts = 0
	m.snapshot = nil
	m.scanned = 0
	m.state = Idle
	return nil
}

// ---------------------------------------------------------------------------
// 对拍驱动：同一操作序列在真实系统与朴素模拟上同步重放，逐步比对。
// ---------------------------------------------------------------------------

type op struct {
	kind string // "put" "del" "start" "step" "cutover" "abort"
	id   string
	body string
	num  int // start 的 B 或 cutover 的 tol
}

func (o op) String() string {
	switch o.kind {
	case "put":
		return fmt.Sprintf("Put(%q,%q)", o.id, o.body)
	case "del":
		return fmt.Sprintf("Del(%q)", o.id)
	case "start":
		return fmt.Sprintf("Start(%d)", o.num)
	case "step":
		return "Step()"
	case "cutover":
		return fmt.Sprintf("Cutover(%d)", o.num)
	case "abort":
		return "Abort()"
	}
	return "?"
}

func canonDest(recs map[string]dest.Record) map[string]string {
	out := make(map[string]string, len(recs))
	for id, r := range recs {
		out[id] = fmt.Sprintf("ver=%d body=%q tomb=%t incompat=%t", r.Ver, r.Body, r.Tombstone, r.Incompatible)
	}
	return out
}

func canonModel(m *model) map[string]string {
	out := make(map[string]string, len(m.recs))
	for id, r := range m.recs {
		out[id] = fmt.Sprintf("ver=%d body=%q tomb=%t incompat=%t", r.ver, r.body, r.tomb, r.incompat)
	}
	return out
}

func canonDocs(docs []source.Doc) map[string]string {
	out := make(map[string]string, len(docs))
	for _, d := range docs {
		out[d.ID] = fmt.Sprintf("seq=%d body=%q", d.Seq, d.Body)
	}
	return out
}

func canonModelDocs(m *model) map[string]string {
	out := make(map[string]string, len(m.docs))
	for id, d := range m.docs {
		out[id] = fmt.Sprintf("seq=%d body=%q", d.Seq, d.Body)
	}
	return out
}

// runSequence 重放 ops 并逐步比对真实系统与朴素模拟，返回终态指纹。
func runSequence(t *testing.T, limit int, ops []op, label string) string {
	t.Helper()
	src, d, c := newSys(t, limit)
	m := newModel(limit)
	for i, o := range ops {
		ctx := fmt.Sprintf("%s 第%d步 %s", label, i, o)
		switch o.kind {
		case "put":
			seqR, errR := src.Put(o.id, []byte(o.body))
			seqM, errM := m.put(o.id, o.body)
			if seqR != seqM || !errors.Is(errR, errM) {
				t.Errorf("%s: 实得(seq=%d,err=%v) 模拟(seq=%d,err=%v)", ctx, seqR, errR, seqM, errM)
			}
		case "del":
			seqR, errR := src.Delete(o.id)
			seqM, errM := m.del(o.id)
			if seqR != seqM || !errors.Is(errR, errM) {
				t.Errorf("%s: 实得(seq=%d,err=%v) 模拟(seq=%d,err=%v)", ctx, seqR, errR, seqM, errM)
			}
		case "start":
			if errR, errM := c.Start(o.num), m.start(o.num); !errors.Is(errR, errM) {
				t.Errorf("%s: 实得 err=%v 模拟 err=%v", ctx, errR, errM)
			}
		case "step":
			aR, cfR, iR, dR, errR := c.Step()
			aM, cfM, iM, dM, errM := m.step()
			if aR != aM || cfR != cfM || iR != iM || dR != dM || !errors.Is(errR, errM) {
				t.Errorf("%s: 实得(%d,%d,%d,%v,%v) 模拟(%d,%d,%d,%v,%v)",
					ctx, aR, cfR, iR, dR, errR, aM, cfM, iM, dM, errM)
			}
		case "cutover":
			if errR, errM := c.Cutover(o.num), m.cutover(o.num); !errors.Is(errR, errM) {
				t.Errorf("%s: 实得 err=%v 模拟 err=%v", ctx, errR, errM)
			}
		case "abort":
			if errR, errM := c.Abort(), m.abort(); !errors.Is(errR, errM) {
				t.Errorf("%s: 实得 err=%v 模拟 err=%v", ctx, errR, errM)
			}
		}
	}
	// 终态比对：dest 全量记录、累计冲突数、源存活文档、协调器状态。
	if got, want := canonDest(d.All()), canonModel(m); !reflect.DeepEqual(got, want) {
		t.Errorf("%s: dest 终态\n实得 %v\n模拟 %v", label, got, want)
	}
	if got := d.Conflicts(); got != m.conflicts {
		t.Errorf("%s: 累计冲突 实得 %d 模拟 %d", label, got, m.conflicts)
	}
	if got, want := canonDocs(src.LiveDocs()), canonModelDocs(m); !reflect.DeepEqual(got, want) {
		t.Errorf("%s: 源终态\n实得 %v\n模拟 %v", label, got, want)
	}
	if got := c.State(); got != m.state {
		t.Errorf("%s: 状态 实得 %v 模拟 %v", label, got, m.state)
	}
	return fmt.Sprintf("dest=%v src=%v conflicts=%d state=%v",
		canonDest(d.All()), canonDocs(src.LiveDocs()), d.Conflicts(), c.State())
}

// TestInterleavingsExhaustive 对 B=1..5、每两次源写入之间插入 0..2 次 Step
// 的全部交错（3^5 种间隙模式 × 5 种 B），与朴素模拟逐步对拍。
func TestInterleavingsExhaustive(t *testing.T) {
	writes := []op{
		{kind: "put", id: "a", body: "1"},     // 命中快照
		{kind: "put", id: "b", body: "22"},    // 命中快照
		{kind: "del", id: "a"},                // 删除已回填/未回填的 a
		{kind: "put", id: "c", body: "33333"}, // 超 L=4，不兼容
		{kind: "del", id: "zz"},               // 不存在，被拒不占号
		{kind: "put", id: "b", body: "4"},     // 再次写 b
	}
	total := 0
	for B := 1; B <= 5; B++ {
		for mask := 0; mask < 243; mask++ { // 3^5 种间隙模式
			ops := []op{
				{kind: "put", id: "a", body: "0"},
				{kind: "put", id: "b", body: "0"},
				{kind: "start", num: B},
			}
			x := mask
			for i, w := range writes {
				ops = append(ops, w)
				if i < len(writes)-1 {
					n := x % 3
					x /= 3
					for k := 0; k < n; k++ {
						ops = append(ops, op{kind: "step"})
					}
				}
			}
			for k := 0; k < 20; k++ { // 跑完回填
				ops = append(ops, op{kind: "step"})
			}
			ops = append(ops, op{kind: "cutover", num: MaxTol})
			label := fmt.Sprintf("B=%d 间隙模式=%d", B, mask)
			t.Run(label, func(t *testing.T) {
				fp := runSequence(t, 4, ops, label)
				t.Logf("输入=%v 输出指纹=%s 判定依据=逐步与朴素模拟一致", ops, fp)
			})
			total++
		}
	}
	t.Logf("共对拍 %d 种交错", total)
}

// TestRandomSequences 1500 组随机序列与朴素模拟对拍，并验证重放一致性。
func TestRandomSequences(t *testing.T) {
	const N = 1500
	ids := []string{"a", "b", "c", "d", "e"}
	randWrite := func(rng *rand.Rand) op {
		id := ids[rng.Intn(len(ids))]
		if rng.Intn(3) == 0 {
			return op{kind: "del", id: id}
		}
		return op{kind: "put", id: id, body: strings.Repeat("x", rng.Intn(12))}
	}
	for i := 0; i < N; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		limit := 1 + rng.Intn(8)
		B := 1 + rng.Intn(5)
		var ops []op
		for k := 0; k < rng.Intn(4); k++ { // 快照前的种子写入
			ops = append(ops, randWrite(rng))
		}
		ops = append(ops, op{kind: "start", num: B})
		nW := 4 + rng.Intn(10)
		for w := 0; w < nW; w++ {
			ops = append(ops, randWrite(rng))
			if w < nW-1 { // 每两次源写入之间插入 0..2 次 Step
				for k := 0; k < rng.Intn(3); k++ {
					ops = append(ops, op{kind: "step"})
				}
			}
		}
		for k := 0; k < 30; k++ { // 跑完回填
			ops = append(ops, op{kind: "step"})
		}
		// 用模拟算出失败集合大小，构造 tol 小 1（拒绝）与恰等（通过）。
		m := newModel(limit)
		for _, o := range ops {
			switch o.kind {
			case "put":
				m.put(o.id, o.body)
			case "del":
				m.del(o.id)
			case "start":
				m.start(o.num)
			case "step":
				m.step()
			}
		}
		f := len(m.failures())
		if f > 0 {
			ops = append(ops, op{kind: "cutover", num: f - 1}) // 小 1 → ErrTooManyFailures
		}
		ops = append(ops, op{kind: "cutover", num: f}) // 恰等 → 通过
		label := fmt.Sprintf("随机序列#%d limit=%d B=%d", i, limit, B)
		fp1 := runSequence(t, limit, ops, label)
		fp2 := runSequence(t, limit, ops, label+"(重放)")
		if fp1 != fp2 {
			t.Errorf("%s: 相同序列重放结果不同\n第一次 %s\n第二次 %s", label, fp1, fp2)
		}
		t.Logf("%s 输入=%v 输出指纹=%s 判定依据=失败集合%d个: tol=%d 拒绝(若>0)、tol=%d 通过；逐步与朴素模拟一致",
			label, ops, fp1, f, f-1, f)
	}
}

// TestConcurrentSmoke 并发调用冒烟：所有操作等价于某个串行顺序，
// 切换成功时不变量成立。
func TestConcurrentSmoke(t *testing.T) {
	src, d, c := newSys(t, 8)
	ids := []string{"a", "b", "c", "d", "e", "f"}
	for _, id := range ids {
		mustPut(t, src, id, "seed")
	}
	if err := c.Start(3); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				id := ids[rng.Intn(len(ids))]
				if rng.Intn(4) == 0 {
					src.Delete(id)
				} else {
					src.Put(id, []byte(strings.Repeat("x", rng.Intn(12))))
				}
			}
		}(int64(g))
	}
	for {
		_, _, _, done, err := c.Step()
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	wg.Wait()
	failures := d.Failures()
	if err := c.Cutover(MaxTol); err != nil {
		t.Fatal(err)
	}
	checkCutoverInvariant(t, src, d, failures, "并发冒烟")
}
