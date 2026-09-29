package rollup

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// sp 返回字符串指针；nil 表示空值（真实的分组取值）。
func sp(v string) *string { return &v }

func dimString(v *string) string {
	if v == nil {
		return "<NULL>"
	}
	return fmt.Sprintf("%q", *v)
}

func printInput(t *testing.T, inc Increment) {
	t.Helper()
	t.Logf("  输入增量: op=%s row{id=%q dim1=%s dim2=%s value=%d}",
		inc.Op, inc.Row.ID, dimString(inc.Row.Dim1), dimString(inc.Row.Dim2), inc.Row.Value)
}

func printEntry(t *testing.T, e LogEntry) {
	t.Helper()
	t.Logf("  输出日志: seq=%d op=%s row=%q", e.Seq, e.Op, e.Row.ID)
	for _, c := range e.Changes {
		t.Logf("    L%d %-8s key={dim1:%s dim2:%s} 变更=%+d行/%+d值 -> 计数=%d 求和=%d",
			c.Layer, c.Layer, key1Desc(c.Key), key2Desc(c.Key),
			c.CountDelta, c.SumDelta, c.ResultCount, c.ResultSum)
	}
}

func key1Desc(k GroupKey) string {
	if !k.HasDim1 {
		return "<占位>"
	}
	return fmt.Sprintf("%q", k.Dim1)
}

func key2Desc(k GroupKey) string {
	if k.Layer != LayerDetail {
		return "-"
	}
	if !k.HasDim2 {
		return "<NULL>"
	}
	return fmt.Sprintf("%q", k.Dim2)
}

func printState(t *testing.T, s *Store, reason string) {
	t.Helper()
	t.Logf("  判定依据[%s]: 行数=%d 总计=%+v", reason, len(s.Rows()), s.Total())
	for _, g := range s.DetailGroups() {
		t.Logf("    L1 明细 dim1=%s dim2=%s => count=%d sum=%d",
			key1Desc(g.Key), key2Desc(g.Key), g.Count, g.Sum)
	}
	for _, g := range s.Subtotals() {
		t.Logf("    L2 小计 dim1=%s => count=%d sum=%d", key1Desc(g.Key), g.Count, g.Sum)
	}
}

type stateSnap struct {
	rows   []Row
	detail []GroupView
	sub    []GroupView
	total  Stats
	logLen int
}

func snapshot(s *Store) stateSnap {
	return stateSnap{s.Rows(), s.DetailGroups(), s.Subtotals(), s.Total(), len(s.Log())}
}

func assertStateUnchanged(t *testing.T, s *Store, before stateSnap) {
	t.Helper()
	after := snapshot(s)
	if len(after.rows) != len(before.rows) {
		t.Fatalf("拒绝后行集变化: before=%d after=%d", len(before.rows), len(after.rows))
	}
	if len(after.detail) != len(before.detail) {
		t.Fatalf("拒绝后明细层变化: before=%d after=%d", len(before.detail), len(after.detail))
	}
	if len(after.sub) != len(before.sub) {
		t.Fatalf("拒绝后小计层变化: before=%d after=%d", len(before.sub), len(after.sub))
	}
	if after.total != before.total {
		t.Fatalf("拒绝后总计变化: before=%+v after=%+v", before.total, after.total)
	}
	if after.logLen != before.logLen {
		t.Fatalf("拒绝后日志长度变化: before=%d after=%d", before.logLen, after.logLen)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("拒绝后自检失败: %v", err)
	}
}

func equalViews(a, b []GroupView) bool {
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

func TestIncrementalLifecycle(t *testing.T) {
	s := New(0)

	steps := []Increment{
		{OpAdd, Row{"r1", sp("A"), sp("x"), 10}},
		{OpAdd, Row{"r2", sp("A"), nil, -10}}, // dim2 空值真实分组
		{OpAdd, Row{"r3", sp("A"), sp("x"), 5}},
		{OpAdd, Row{"r4", nil, sp("y"), 7}}, // dim1 空值真实分组
		{OpRemove, Row{ID: "r3"}},           // 撤回：明细组计数归零应删除
	}

	t.Log("== 一、正常增量：空值分组 / 求和为零保留 / 撤回归零删除 ==")
	for _, inc := range steps {
		printInput(t, inc)
		e, err := s.Apply(inc)
		if err != nil {
			t.Fatalf("合法增量被拒绝: %v", err)
		}
		printEntry(t, e)
		if err := s.Verify(); err != nil {
			t.Fatalf("应用 %q 后三层不自洽: %v", inc.Row.ID, err)
		}
	}
	printState(t, s, "最终状态")

	// 小计 A：r1(10)+r2(-10)=0，count=2 sum=0 —— 求和为零但计数>0必须保留。
	var foundA bool
	for _, g := range s.Subtotals() {
		if g.Key.HasDim1 && g.Key.Dim1 == "A" {
			foundA = true
			if g.Count != 2 || g.Sum != 0 {
				t.Fatalf("小计 A 期望 count=2 sum=0（求和为零保留），得到 count=%d sum=%d", g.Count, g.Sum)
			}
		}
	}
	if !foundA {
		t.Fatal("求和为零的小计组 A 被错误删除")
	}

	var detailX int64
	for _, g := range s.DetailGroups() {
		if g.Key.Dim1 == "A" && g.Key.HasDim2 && g.Key.Dim2 == "x" {
			detailX = g.Count
		}
	}
	if detailX != 1 {
		t.Fatalf("明细组 (A,x) 撤回 r3 后期望计数 1，得到 %d", detailX)
	}

	if total := s.Total(); total.Count != 3 || total.Sum != 7 { // 10-10+7
		t.Fatalf("总计期望 {3 7}，得到 %+v", total)
	}

	t.Log("== 二、计数>0但求和为零的明细组必须保留 ==")
	printInput(t, Increment{OpAdd, Row{"r5", sp("B"), sp("z"), 0}})
	e, err := s.Apply(Increment{OpAdd, Row{"r5", sp("B"), sp("z"), 0}})
	if err != nil {
		t.Fatal(err)
	}
	printEntry(t, e)
	var foundBZ bool
	for _, g := range s.DetailGroups() {
		if g.Key.Dim1 == "B" && g.Key.Dim2 == "z" && g.Count == 1 && g.Sum == 0 {
			foundBZ = true
		}
	}
	if !foundBZ {
		t.Fatal("计数>0求和=0 的明细组必须保留")
	}

	t.Log("== 三、撤回 r5：计数归零，只输出撤回且组删除 ==")
	printInput(t, Increment{OpRemove, Row{ID: "r5"}})
	e, err = s.Apply(Increment{OpRemove, Row{ID: "r5"}})
	if err != nil {
		t.Fatal(err)
	}
	printEntry(t, e)
	for _, c := range e.Changes {
		if c.CountDelta != -1 {
			t.Fatalf("撤回变更计数应为 -1: %+v", c)
		}
		if c.Layer != LayerGrand && c.ResultCount != 0 {
			t.Fatalf("明细/小计撤回后计数应为 0（仅输出撤回）: %+v", c)
		}
	}
	for _, g := range s.DetailGroups() {
		if g.Key.Dim1 == "B" {
			t.Fatal("计数归零的明细组应已删除")
		}
	}

	t.Log("== 四、非法输入：四类可区分原因，拒绝不留痕 ==")
	before := snapshot(s)
	bad := []struct {
		name string
		inc  Increment
		want error
	}{
		{"非法操作类型", Increment{OpUnknown, Row{"x", sp("A"), sp("x"), 1}}, ErrInvalidIncrement},
		{"空行标识", Increment{OpAdd, Row{"", sp("A"), sp("x"), 1}}, ErrInvalidIncrement},
		{"新增重复行", Increment{OpAdd, Row{"r1", sp("A"), sp("x"), 1}}, ErrDuplicateRow},
		{"撤回不存在的行", Increment{OpRemove, Row{ID: "ghost"}}, ErrRowNotFound},
	}
	for _, tc := range bad {
		t.Logf("  -- 场景: %s", tc.name)
		printInput(t, tc.inc)
		if _, err := s.Apply(tc.inc); !errors.Is(err, tc.want) {
			t.Fatalf("%s: 期望错误 %v，得到 %v", tc.name, tc.want, err)
		}
		t.Logf("  输出: 拒绝（%v）；判定依据: 无日志追加、三层快照与拒绝前逐字段相等", err)
		assertStateUnchanged(t, s, before)
	}

	errCats := []error{ErrInvalidIncrement, ErrDuplicateRow, ErrRowNotFound, ErrTooManyGroups}
	for i := range errCats {
		for j := i + 1; j < len(errCats); j++ {
			if errors.Is(errCats[i], errCats[j]) {
				t.Fatalf("错误类别必须互不相同: %v == %v", errCats[i], errCats[j])
			}
		}
	}

	t.Log("== 五、明细组数超限：整体拒绝且不留痕 ==")
	small := New(2)
	for _, inc := range []Increment{
		{OpAdd, Row{"a", sp("A"), sp("x"), 1}},
		{OpAdd, Row{"b", sp("A"), sp("y"), 1}},
	} {
		if _, err := small.Apply(inc); err != nil {
			t.Fatal(err)
		}
	}
	beforeSmall := snapshot(small)
	over := Increment{OpAdd, Row{"c", sp("B"), sp("z"), 1}}
	printInput(t, over)
	if _, err := small.Apply(over); !errors.Is(err, ErrTooManyGroups) {
		t.Fatalf("超限应拒绝为 ErrTooManyGroups，得到 %v", err)
	}
	t.Logf("  输出: 拒绝（%v）；判定依据: 明细组数已达上限 2", ErrTooManyGroups)
	assertStateUnchanged(t, small, beforeSmall)

	same := Increment{OpAdd, Row{"c2", sp("A"), sp("x"), 1}}
	printInput(t, same)
	if _, err := small.Apply(same); err != nil {
		t.Fatalf("复用已有明细组不应触发上限: %v", err)
	}
	t.Log("  判定依据: 落入已存在明细组不新增组数，允许提交")

	t.Log("== 六、与批量重算一致 + 每个日志前缀自洽 ==")
	if err := s.Verify(); err != nil {
		t.Fatalf("最终自检失败: %v", err)
	}
	batch := BatchRecompute(s.Rows())
	if batch.Total() != s.Total() {
		t.Fatalf("批量重算总计不一致: batch=%+v inc=%+v", batch.Total(), s.Total())
	}
	if !equalViews(batch.DetailGroups(), s.DetailGroups()) {
		t.Fatalf("批量重算明细层不一致:\n batch=%+v\n inc=%+v", batch.DetailGroups(), s.DetailGroups())
	}
	if !equalViews(batch.Subtotals(), s.Subtotals()) {
		t.Fatalf("批量重算小计层不一致:\n batch=%+v\n inc=%+v", batch.Subtotals(), s.Subtotals())
	}
	t.Log("  判定依据: 增量三层视图与对最终行集批量重算逐层相等")

	r := NewReplay()
	logEntries := s.Log()
	for _, e := range logEntries {
		r.ApplyEntry(e)
		if err := r.checkInvariant(); err != nil {
			t.Fatalf("日志前缀 seq=%d 不自洽: %v", e.Seq, err)
		}
	}
	if !equalViews(r.DetailGroups(), s.DetailGroups()) ||
		!equalViews(r.Subtotals(), s.Subtotals()) ||
		r.Total() != s.Total() {
		t.Fatal("日志全量重放结果与当前状态不一致")
	}
	t.Logf("  判定依据: 全部 %d 条日志按序重放，逐前缀自洽且最终一致", len(logEntries))

	t.Log("== 七、空值语义：NULL 与汇总占位严格区分 ==")
	var nullDim1, nullDim2 bool
	for _, g := range s.DetailGroups() {
		if !g.Key.HasDim1 && g.Key.HasDim2 && g.Key.Dim2 == "y" {
			nullDim1 = true // r4: (NULL, y)
		}
		if g.Key.HasDim1 && g.Key.Dim1 == "A" && !g.Key.HasDim2 {
			nullDim2 = true // r2: (A, NULL)
		}
		if !g.Key.HasDim1 && !g.Key.HasDim2 {
			t.Fatal("明细层不得出现汇总占位键")
		}
	}
	if !nullDim1 || !nullDim2 {
		t.Fatal("空值必须作为真实取值各自成组")
	}
}

func TestConcurrent(t *testing.T) {
	s := New(0)
	const writers = 8
	const perWriter = 50
	var writerWg, readerWg sync.WaitGroup

	// 写者：使用互不相交的 id，每 add 两行后 remove 一行（撤回真实存在的行）。
	for w := 0; w < writers; w++ {
		writerWg.Add(1)
		go func(w int) {
			defer writerWg.Done()
			dim := fmt.Sprintf("D%d", w)
			for i := 0; i < perWriter; i++ {
				id := fmt.Sprintf("w%d-%d", w, i)
				var d2 *string
				if i%2 == 0 {
					v := fmt.Sprintf("k%d", i%3)
					d2 = &v
				} // 奇数行 dim2 为 NULL
				if _, err := s.Apply(Increment{OpAdd, Row{id, &dim, d2, int64(i - 25)}}); err != nil {
					t.Errorf("add %s: %v", id, err)
					return
				}
				if i%4 == 0 {
					// 立即撤回上一步加入的行（删除只给行标识）
					if _, err := s.Apply(Increment{OpRemove, Row{ID: id}}); err != nil {
						t.Errorf("remove %s: %v", id, err)
						return
					}
				}
			}
		}(w)
	}

	// 读者：提交视图查询与自检并发调用，全程不得读到撕裂状态。
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.DetailGroups()
					_ = s.Subtotals()
					_ = s.Total()
					_ = s.Rows()
					_ = s.Log()
					if err := s.Verify(); err != nil {
						t.Errorf("并发自检失败: %v", err)
						return
					}
				}
			}
		}()
	}

	writerWg.Wait()
	close(stop)
	readerWg.Wait()

	if err := s.Verify(); err != nil {
		t.Fatalf("并发结束后自检失败: %v", err)
	}

	// i%4==0 时加入后立即撤回：i 取 0,4,...,48 共 13 个。
	removedPerWriter := 0
	for i := 0; i < perWriter; i++ {
		if i%4 == 0 {
			removedPerWriter++
		}
	}
	alive := writers * (perWriter - removedPerWriter)
	if got := len(s.Rows()); got != alive {
		t.Fatalf("并发后期望存活 %d 行，得到 %d", alive, got)
	}
	batch := BatchRecompute(s.Rows())
	if !equalViews(batch.DetailGroups(), s.DetailGroups()) ||
		!equalViews(batch.Subtotals(), s.Subtotals()) ||
		batch.Total() != s.Total() {
		t.Fatal("并发后增量结果与批量重算不一致")
	}
	t.Logf("并发判定依据: 全程 Verify 通过；存活 %d 行；三层与批量重算一致；-race 无告警", alive)
}
