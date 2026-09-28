package dedup

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// applyAndLog 执行一批变更，并把输入条目、输出条目与判定依据打印到测试日志。
func applyAndLog(t *testing.T, c *Counter, label string, entries []Entry) ([]GroupChange, error) {
	t.Helper()
	t.Logf("── 用例 %s：输入 %d 条 %v", label, len(entries), entries)
	out, err := c.Apply(entries)
	if err != nil {
		be := err.(*BatchError)
		t.Logf("   判定：拒绝整批 → reason=%q index=%d (%s)；依据：%s",
			be.Reason, be.Index, be.Error(), be.Reason.Explain())
		return nil, err
	}
	t.Logf("   判定：接受，输出 %d 条组变化 %v", len(out), out)
	return out, nil
}

// rejectAt 断言批被拒绝，且原因、下标与预期一致。
func rejectAt(t *testing.T, err error, want RejectReason, wantIndex int) {
	t.Helper()
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("预期 *BatchError，得到 %T: %v", err, err)
	}
	if be.Reason != want {
		t.Fatalf("拒绝原因 = %q，预期 %q", be.Reason, want)
	}
	if be.Index != wantIndex {
		t.Fatalf("拒绝下标 = %d，预期 %d", be.Index, wantIndex)
	}
}

// replay 模拟下游：按日志顺序累加每批的 Δ，重放出去重计数视图。
// 约定：某组计数折叠到 0 时该组即不存在（与 Snapshot 的规范表示一致）。
func replay(log [][]GroupChange) map[string]int {
	view := make(map[string]int)
	for _, batch := range log {
		for _, ch := range batch {
			n := view[ch.Group] + ch.Delta()
			if n == 0 {
				delete(view, ch.Group)
			} else {
				view[ch.Group] = n
			}
		}
	}
	return view
}

// TestMultiplicitySameValue 覆盖同一值多次插入与撤回：多重性可大于 1，
// 但去重计数只计"次数为正的值个数"。
func TestMultiplicitySameValue(t *testing.T) {
	c := NewCounter(100)

	out, err := applyAndLog(t, c, "同值插入3次", []Entry{
		{Group: "g", Value: "v", Delta: 1},
		{Group: "g", Value: "v", Delta: 1},
		{Group: "g", Value: "v", Delta: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, []GroupChange{{Group: "g", Before: 0, After: 1}}) {
		t.Fatalf("多重性=3 但去重计数应为 1，得到 %v", out)
	}

	// 撤回 2 次后多重性为 1，去重计数仍为 1；再撤回 1 次归零，计数变 0。
	out, err = applyAndLog(t, c, "同值撤回2次", []Entry{
		{Group: "g", Value: "v", Delta: -1},
		{Group: "g", Value: "v", Delta: -1},
	})
	if err != nil || !reflect.DeepEqual(out, []GroupChange{{Group: "g", Before: 1, After: 1}}) {
		t.Fatalf("撤回2次后计数应保持 1，得到 %v, %v", out, err)
	}
	out, err = applyAndLog(t, c, "末次撤回", []Entry{{Group: "g", Value: "v", Delta: -1}})
	if err != nil || !reflect.DeepEqual(out, []GroupChange{{Group: "g", Before: 1, After: 0}}) {
		t.Fatalf("撤回至零后计数应为 0，得到 %v, %v", out, err)
	}
	if got := c.Snapshot(); len(got) != 0 {
		t.Fatalf("组清空后应从视图移除，得到 %v", got)
	}

	// 多重性已为零，再撤回必须拒绝。
	_, err = applyAndLog(t, c, "撤回已归零的值", []Entry{{Group: "g", Value: "v", Delta: -1}})
	rejectAt(t, err, ReasonWithdrawZero, 0)
}

// TestBatchFoldAddThenRemove 覆盖批内先增后删：同一批内插入与撤回折叠后
// 只输出净变化；以及多值交错时按值分别计贡献。
func TestBatchFoldAddThenRemove(t *testing.T) {
	c := NewCounter(100)

	// 批内：v1 插入两次又撤回两次（折叠为 0），v2 插入一次。
	out, err := applyAndLog(t, c, "批内先增后删", []Entry{
		{Group: "g", Value: "v1", Delta: 1},
		{Group: "g", Value: "v1", Delta: 1},
		{Group: "g", Value: "v2", Delta: 1},
		{Group: "g", Value: "v1", Delta: -1},
		{Group: "g", Value: "v1", Delta: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, []GroupChange{{Group: "g", Before: 0, After: 1}}) {
		t.Fatalf("折叠后仅 v2 留存，After 应为 1，得到 %v", out)
	}

	// 跨组的批按组名字典序输出，Before/After 各自独立。
	out, err = applyAndLog(t, c, "多组折叠", []Entry{
		{Group: "b", Value: "x", Delta: 1},
		{Group: "a", Value: "y", Delta: 1},
		{Group: "a", Value: "y", Delta: -1}, // a 组净 0，仍出现在输出中
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []GroupChange{
		{Group: "a", Before: 0, After: 0},
		{Group: "b", Before: 0, After: 1},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("输出应按组名字典序且包含净0组，得到 %v，预期 %v", out, want)
	}
}

// TestBatchOrdering 覆盖批内顺序敏感性：撤回在"此前各条已生效"的状态上
// 校验，所以先撤回后插入会拒绝，而先插入后撤回接受（两者折叠值相同）。
func TestBatchOrdering(t *testing.T) {
	// 顺序 A：插入后撤回 —— 合法，净 0。
	c1 := NewCounter(100)
	out, err := applyAndLog(t, c1, "顺序[插,撤]", []Entry{
		{Group: "g", Value: "v", Delta: 1},
		{Group: "g", Value: "v", Delta: -1},
	})
	if err != nil || out[0].After != 0 {
		t.Fatalf("[插,撤] 应接受且净0，得到 %v, %v", out, err)
	}

	// 顺序 B（颠倒）：撤回时此前无插入，多重性为零 → 拒绝整批。
	c2 := NewCounter(100)
	_, err = applyAndLog(t, c2, "顺序[撤,插]", []Entry{
		{Group: "g", Value: "v", Delta: -1},
		{Group: "g", Value: "v", Delta: 1},
	})
	rejectAt(t, err, ReasonWithdrawZero, 0)
	if got := c2.Snapshot(); len(got) != 0 {
		t.Fatalf("被拒绝的批不得改变视图，得到 %v", got)
	}
	if len(c2.Log()) != 0 {
		t.Fatalf("被拒绝的批不得产生日志")
	}

	// 颠倒顺序但已有已提交插入兜底：第一条撤回作用在多重性 1 上，合法。
	c3 := NewCounter(100)
	if _, err := c3.Apply([]Entry{{Group: "g", Value: "v", Delta: 1}}); err != nil {
		t.Fatal(err)
	}
	out, err = applyAndLog(t, c3, "颠倒但有存量", []Entry{
		{Group: "g", Value: "v", Delta: -1}, // 1 -> 0
		{Group: "g", Value: "v", Delta: 1},  // 0 -> 1
	})
	if err != nil || out[0].Before != 1 || out[0].After != 1 {
		t.Fatalf("有存量时颠倒顺序应接受且计数不变，得到 %v, %v", out, err)
	}
}

// TestInvalidInputs 覆盖各类非法输入及其可区分原因，并验证拒绝原子性。
func TestInvalidInputs(t *testing.T) {
	good := Entry{Group: "g", Value: "v", Delta: 1}

	cases := []struct {
		name    string
		max     int
		entries []Entry
		reason  RejectReason
		index   int
	}{
		{"空组名", 100, []Entry{good, {Group: "", Value: "v", Delta: 1}}, ReasonEmptyGroup, 1},
		{"空值", 100, []Entry{{Group: "g", Value: "", Delta: 1}}, ReasonEmptyValue, 0},
		{"符号为0", 100, []Entry{{Group: "g", Value: "v", Delta: 0}}, ReasonInvalidSign, 0},
		{"符号为+2", 100, []Entry{{Group: "g", Value: "v", Delta: 2}}, ReasonInvalidSign, 0},
		{"符号为-2", 100, []Entry{{Group: "g", Value: "v", Delta: -2}}, ReasonInvalidSign, 0},
		{"撤回不存在的值", 100, []Entry{{Group: "g", Value: "ghost", Delta: -1}}, ReasonWithdrawZero, 0},
		{"条目数超限", 1, []Entry{good, good}, ReasonTooManyEntries, -1},
	}
	for _, tc := range cases {
		c := NewCounter(tc.max)
		// 先制造一点已提交状态，用于验证拒绝后状态不变。
		if _, err := c.Apply([]Entry{good}); err != nil {
			t.Fatal(err)
		}
		_, err := applyAndLog(t, c, tc.name, tc.entries)
		rejectAt(t, err, tc.reason, tc.index)

		got := c.Snapshot()
		if !reflect.DeepEqual(got, map[string]int{"g": 1}) {
			t.Fatalf("%s：拒绝后多重性/视图被改变，得到 %v", tc.name, got)
		}
		if log := c.Log(); len(log) != 1 {
			t.Fatalf("%s：拒绝不得追加日志，得到 %d 条", tc.name, len(log))
		}
	}

	// 批内前段合法、后段非法：前段的效果必须随整批回滚。
	c := NewCounter(100)
	_, err := applyAndLog(t, c, "前段合法后段非法", []Entry{
		{Group: "g", Value: "a", Delta: 1},
		{Group: "g", Value: "a", Delta: 1},
		{Group: "g", Value: "b", Delta: -1}, // b 不存在
	})
	rejectAt(t, err, ReasonWithdrawZero, 2)
	if got := c.Snapshot(); len(got) != 0 {
		t.Fatalf("整批回滚后视图应为空，得到 %v", got)
	}
}

// TestDeterministic 验证同一输入序列反复计算得到完全相同的输出。
func TestDeterministic(t *testing.T) {
	seq := [][]Entry{
		{{Group: "g", Value: "a", Delta: 1}, {Group: "g", Value: "b", Delta: 1}},
		{{Group: "g", Value: "a", Delta: 1}, {Group: "g", Value: "a", Delta: -1}},
		{{Group: "g", Value: "b", Delta: -1}},
		{{Group: "h", Value: "x", Delta: 1}, {Group: "h", Value: "x", Delta: -1}},
		{{Group: "g", Value: "a", Delta: -1}},
	}
	var first [][]GroupChange
	var firstSnap map[string]int
	for run := 0; run < 5; run++ {
		c := NewCounter(100)
		var cur [][]GroupChange
		for i, batch := range seq {
			out, err := applyAndLog(t, c, fmt.Sprintf("确定性第%d轮/批%d", run, i), batch)
			if err != nil {
				t.Fatal(err)
			}
			cur = append(cur, out)
		}
		snap := c.Snapshot()
		if run == 0 {
			first, firstSnap = cur, snap
		} else if !reflect.DeepEqual(cur, first) || !reflect.DeepEqual(snap, firstSnap) {
			t.Fatalf("第 %d 轮输出与首轮不一致:\n%v\n%v", run, cur, snap)
		}
	}
}

// TestLogReplay 验证下游按顺序应用日志始终得到与当前视图一致的去重计数。
func TestLogReplay(t *testing.T) {
	c := NewCounter(100)
	batches := [][]Entry{
		{{Group: "orders", Value: "c1", Delta: 1}, {Group: "orders", Value: "c2", Delta: 1}},
		{{Group: "orders", Value: "c1", Delta: 1}}, // c1 多重性 2，计数不变
		{{Group: "orders", Value: "c1", Delta: -1}, {Group: "payments", Value: "p1", Delta: 1}},
		{{Group: "orders", Value: "c2", Delta: -1}},
	}
	for i, b := range batches {
		if _, err := applyAndLog(t, c, fmt.Sprintf("重放/批%d", i), b); err != nil {
			t.Fatal(err)
		}
		if got, want := replay(c.Log()), c.Snapshot(); !reflect.DeepEqual(got, want) {
			t.Fatalf("批 %d 后日志重放 %v 与快照 %v 不一致", i, got, want)
		}
	}
	end, err := c.Apply([]Entry{
		{Group: "orders", Value: "c1", Delta: -1},
		{Group: "payments", Value: "p1", Delta: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("   收尾批输出 %v，最终快照 %v", end, c.Snapshot())
	if got := replay(c.Log()); !reflect.DeepEqual(got, map[string]int{}) {
		t.Fatalf("全部撤回后重放应为空视图，得到 %v", got)
	}
}

// TestConcurrentApplyAndRead 在竞态检测下并发提交与读取：
// 每个写者独占一个组做插/撤配对，读者持续取快照；结束后日志重放必须
// 等于快照（无论提交如何交错，日志顺序应用都得到正确计数）。
func TestConcurrentApplyAndRead(t *testing.T) {
	c := NewCounter(1_000_000)
	const writers = 16
	const rounds = 200

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读者：反复取快照并校验字段合理性（计数非负且不超过写者单组上限）。
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				snap := c.Snapshot()
				for g, n := range snap {
					if n < 0 || n > 2 {
						panic(fmt.Sprintf("%s 快照出现不可能的计数 %d", g, n))
					}
				}
				// 日志同样必须随时可重放出合法视图。
				for g, n := range replay(c.Log()) {
					if n < 0 || n > 2 {
						panic(fmt.Sprintf("%s 日志重放出现不可能的计数 %d", g, n))
					}
				}
			}
		}
	}()

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			g := fmt.Sprintf("g%02d", id)
			for r := 0; r < rounds; r++ {
				// 批内插入两个不同值再撤回其中一个，结束时该组计数为 1。
				if _, err := c.Apply([]Entry{
					{Group: g, Value: "a", Delta: 1},
					{Group: g, Value: "b", Delta: 1},
					{Group: g, Value: "b", Delta: -1},
				}); err != nil {
					panic(err)
				}
				// 撤回最后一个值，组计数归 0。
				if _, err := c.Apply([]Entry{{Group: g, Value: "a", Delta: -1}}); err != nil {
					panic(err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)

	final := c.Snapshot()
	if len(final) != 0 {
		t.Fatalf("所有插/撤配对结束后快照应为空，得到 %v", final)
	}
	if got := replay(c.Log()); !reflect.DeepEqual(got, final) {
		t.Fatalf("并发结束后日志重放 %v 与快照 %v 不一致", got, final)
	}
	if want := writers * rounds * 2; len(c.Log()) != want {
		t.Fatalf("日志条数 = %d，预期 %d", len(c.Log()), want)
	}
}
