package ontology

import (
	"errors"
	"fmt"
	"testing"
)

// recompute 是测试参照实现：无视增量日志，直接对最终行表做批量分组聚合。
// 增量视图必须始终与它一致。
func recompute(rows map[string]Row) []GroupView {
	type acc struct {
		sum   int64
		count int64
	}
	m := make(map[string]*acc)
	for _, r := range rows {
		a := m[r.Group]
		if a == nil {
			a = &acc{}
			m[r.Group] = a
		}
		a.sum += r.Value
		a.count++
	}
	out := make([]GroupView, 0, len(m))
	for g, a := range m { // 行表中不存在空分组键，所有组计数必为正
		out = append(out, GroupView{Group: g, Sum: a.sum, Count: a.count})
	}
	sortViews(out)
	return out
}

// sortViews 在测试本地排序，避免引入与实现相同的排序代码路径。
func sortViews(v []GroupView) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1].Group > v[j].Group; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}

// logOps / logEntries / logReason 按要求打印测试输入、输出条目与判定依据。
func logOps(t *testing.T, batch int, ops []Op) {
	t.Helper()
	for i, op := range ops {
		t.Logf("输入 批%d[%d] kind=%d rowID=%q group=%q value=%d",
			batch, i, op.Kind, op.RowID, op.Group, op.Value)
	}
}

func logEntries(t *testing.T, entries []LogEntry) {
	t.Helper()
	if len(entries) == 0 {
		t.Logf("输出 (无日志条目)")
		return
	}
	for _, e := range entries {
		kind := "RETRACT"
		if e.Kind == EntryAdd {
			kind = "ADD"
		}
		t.Logf("输出 seq=%d op[%d] row=%q group=%q %s valueΔ=%d countΔ=%d -> sum=%d count=%d",
			e.Seq, e.OpIndex, e.RowID, e.Group, kind, e.Value, e.Count, e.SumAfter, e.CountAfter)
	}
}

func logReason(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("判定依据: "+format, args...)
}

// assertConsistent 校验快照与批量重算一致，且不含计数为零的组。
func assertConsistent(t *testing.T, agg *Aggregator) {
	t.Helper()
	got := agg.Snapshot()
	want := recompute(agg.Rows())
	if len(got) != len(want) {
		t.Fatalf("视图组数不一致: got=%d want=%d (got=%v want=%v)", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("视图与批量重算不一致 @%d: got=%+v want=%+v", i, got[i], want[i])
		}
		if got[i].Count <= 0 {
			t.Fatalf("视图中出现计数非正的组: %+v", got[i])
		}
	}
}

// assertReject 校验操作被指定原因拒绝，且错误可被 errors.Is 区分、携带批次下标。
func assertReject(t *testing.T, agg *Aggregator, ops []Op, index int, cause error) {
	t.Helper()
	beforeRows := agg.Rows()
	beforeView := agg.Snapshot()
	beforeLog := agg.Log()

	_, err := agg.Apply(ops)
	if err == nil {
		t.Fatalf("期望批被拒绝（%v），但成功了", cause)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("拒绝原因不符: got=%v want=%v", err, cause)
	}
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("错误应为 *RejectError, got=%T", err)
	}
	if re.Index != index {
		t.Fatalf("拒绝下标不符: got=%d want=%d", re.Index, index)
	}
	logReason(t, "批在第 %d 条被拒绝：%v（非法操作=%+v）", index, cause, re.Op)

	// 被拒绝的批不得改变行表、聚合与已产生的日志。
	if fmt.Sprint(agg.Rows()) != fmt.Sprint(beforeRows) {
		t.Fatalf("被拒绝的批改变了行表")
	}
	if fmt.Sprint(agg.Snapshot()) != fmt.Sprint(beforeView) {
		t.Fatalf("被拒绝的批改变了聚合视图")
	}
	afterLog := agg.Log()
	if len(afterLog) != len(beforeLog) {
		t.Fatalf("被拒绝的批改变了日志长度: before=%d after=%d", len(beforeLog), len(afterLog))
	}
}
