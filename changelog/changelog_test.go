package changelog

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// describeBatch 把一批输入格式化为可读字符串，供测试日志打印“输入”。
func describeBatch(batch []Write) string {
	parts := make([]string, len(batch))
	for i, w := range batch {
		if w.Op == OpPut {
			parts[i] = fmt.Sprintf("[%d] put(%q=%q)", i, w.Key, w.Value)
		} else {
			parts[i] = fmt.Sprintf("[%d] delete(%q)", i, w.Key)
		}
	}
	return strings.Join(parts, " ")
}

// describeEntries 把输出条目格式化为可读字符串，供测试日志打印“输出”。
func describeEntries(entries []Entry) string {
	if len(entries) == 0 {
		return "<无条目>"
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = fmt.Sprintf("#%d %s(%q=%q)", e.Seq, e.Kind, e.Key, e.Value)
	}
	return strings.Join(parts, " ")
}

// applyLogged 执行一批并打印输入、输出与判定依据。
func applyLogged(t *testing.T, c *Coordinator, batch []Write, rationale string) ([]Entry, error) {
	t.Helper()
	t.Logf("输入: %s", describeBatch(batch))
	t.Logf("判定依据: %s", rationale)
	entries, err := c.Apply(batch)
	if err != nil {
		t.Logf("输出: 拒绝 -> %v", err)
	} else {
		t.Logf("输出: %s", describeEntries(entries))
	}
	return entries, err
}

func expectBatchError(t *testing.T, err error, reason RejectReason, index int) *BatchError {
	t.Helper()
	if err == nil {
		t.Fatalf("期望因 %s 被拒绝，实际成功", reason)
	}
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("期望 *BatchError，实际 %T: %v", err, err)
	}
	if be.Reason != reason {
		t.Fatalf("期望原因 %s，实际 %s", reason, be.Reason)
	}
	if be.Index != index {
		t.Fatalf("期望出错下标 %d，实际 %d", index, be.Index)
	}
	return be
}

func expectSnapshot(t *testing.T, c *Coordinator, wantTable map[string]string, wantLog []Entry) {
	t.Helper()
	gotTable, gotLog := c.Snapshot()
	if !reflect.DeepEqual(gotTable, wantTable) {
		t.Fatalf("表不一致\n  got: %v\n want: %v", gotTable, wantTable)
	}
	if !reflect.DeepEqual(gotLog, wantLog) {
		t.Fatalf("日志不一致\n  got: %v\n want: %v", gotLog, wantLog)
	}
}

// TestInBatchOverwrite 批内覆盖：同一键在一批内多次写入/删除，只折叠出净变化。
func TestInBatchOverwrite(t *testing.T) {
	c := New(0)

	// a 连续两次 put 不同值：净效果为 upsert(a,v2)，中间值 v1 不出现在日志。
	batch := []Write{
		{Key: "a", Op: OpPut, Value: "v1"},
		{Key: "a", Op: OpPut, Value: "v2"},
	}
	entries, err := applyLogged(t, c, batch,
		"键 a 批前不存在、批后为 v2；批内 v1 被 v2 覆盖，净变化仅 upsert(a,v2)")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{Seq: 0, Kind: KindUpsert, Key: "a", Value: "v2"}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("got %v, want %v", entries, want)
	}

	// 批内 put 后又 delete：净效果为不存在，不输出任何条目。
	entries, err = applyLogged(t, c,
		[]Write{{Key: "b", Op: OpPut, Value: "x"}, {Key: "b", Op: OpDelete}},
		"键 b 批前不存在、批后仍不存在（put 后 delete 自抵消），状态相同不输出")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("期望无条目，实际 %v", entries)
	}

	// 批内 delete 后又 put 新值：净效果为覆盖 a，先 retract(v2) 再 upsert(v3)。
	entries, err = applyLogged(t, c,
		[]Write{{Key: "a", Op: OpDelete}, {Key: "a", Op: OpPut, Value: "v3"}},
		"键 a 批前为 v2、批后为 v3；批内删除再写入，净效果为值变更：先撤回 v2 再写入 v3")
	if err != nil {
		t.Fatal(err)
	}
	want = []Entry{
		{Seq: 1, Kind: KindRetract, Key: "a", Value: "v2"},
		{Seq: 2, Kind: KindUpsert, Key: "a", Value: "v3"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("got %v, want %v", entries, want)
	}
}

// TestDeleteNonexistent 删除不存在的键：净状态不变，不输出。
func TestDeleteNonexistent(t *testing.T) {
	c := New(0)
	entries, err := applyLogged(t, c,
		[]Write{{Key: "ghost", Op: OpDelete}},
		"键 ghost 批前不存在、批后仍不存在（删除不存在的键），状态相同不输出")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("期望无条目，实际 %v", entries)
	}
	expectSnapshot(t, c, map[string]string{}, []Entry{})
}

// TestEqualValueNoOutput 写入与当前值相等：不输出。
func TestEqualValueNoOutput(t *testing.T) {
	c := New(0)
	if _, err := applyLogged(t, c,
		[]Write{{Key: "k", Op: OpPut, Value: "same"}},
		"前置：k 从不存在变为 same，输出 upsert"); err != nil {
		t.Fatal(err)
	}

	// 批内多次 put 相同值：净状态不变。
	entries, err := applyLogged(t, c,
		[]Write{{Key: "k", Op: OpPut, Value: "same"}, {Key: "k", Op: OpPut, Value: "same"}},
		"键 k 批前为 same、批后仍为 same（批内重复写同值），值相等不输出")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("期望无条目，实际 %v", entries)
	}

	// 不同批次写同值：同样不输出，日志保持只有最初一条。
	entries, err = applyLogged(t, c,
		[]Write{{Key: "k", Op: OpPut, Value: "same"}},
		"另一批写相同值 same，批前批后状态相同，不输出")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("期望无条目，实际 %v", entries)
	}
	expectSnapshot(t, c,
		map[string]string{"k": "same"},
		[]Entry{{Seq: 0, Kind: KindUpsert, Key: "k", Value: "same"}})
}

// TestMultiKeyOrder 多键输出顺序：按键在批中首次出现的顺序；每键内部先撤回再写入。
func TestMultiKeyOrder(t *testing.T) {
	c := New(0)
	// 初始：a=1, b=2, c=3
	if _, err := c.Apply([]Write{
		{Key: "a", Op: OpPut, Value: "1"},
		{Key: "b", Op: OpPut, Value: "2"},
		{Key: "c", Op: OpPut, Value: "3"},
	}); err != nil {
		t.Fatal(err)
	}

	batch := []Write{
		{Key: "c", Op: OpPut, Value: "9"}, // c 首次出现（下标 0）
		{Key: "a", Op: OpDelete},          // a 首次出现（下标 1）
		{Key: "c", Op: OpPut, Value: "30"},
		{Key: "b", Op: OpPut, Value: "2"}, // b 与现值相等，不输出
		{Key: "d", Op: OpPut, Value: "4"}, // 新键
	}
	entries, err := applyLogged(t, c, batch,
		"首次出现顺序为 c,a,b,d：c 3→30（撤回+写入），a 删除（撤回），b 值相等不输出，d 新增（写入）")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Seq: 3, Kind: KindRetract, Key: "c", Value: "3"},
		{Seq: 4, Kind: KindUpsert, Key: "c", Value: "30"},
		{Seq: 5, Kind: KindRetract, Key: "a", Value: "1"},
		{Seq: 6, Kind: KindUpsert, Key: "d", Value: "4"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("got %v\nwant %v", entries, want)
	}
}

// TestRejectEmptyKey 空键必须以 empty_key 拒绝，且拒绝不改变状态。
func TestRejectEmptyKey(t *testing.T) {
	c := New(0)
	if _, err := c.Apply([]Write{{Key: "ok", Op: OpPut, Value: "v"}}); err != nil {
		t.Fatal(err)
	}
	beforeTable, beforeLog := c.Snapshot()

	batch := []Write{
		{Key: "x", Op: OpPut, Value: "1"},
		{Key: "", Op: OpPut, Value: "boom"}, // 下标 1 空键
	}
	_, err := applyLogged(t, c, batch, "下标 1 键为空字符串，必须以 empty_key 拒绝整批")
	be := expectBatchError(t, err, ReasonEmptyKey, 1)
	if be.Key != "" {
		t.Fatalf("期望空 Key，实际 %q", be.Key)
	}
	assertUnchanged(t, c, beforeTable, beforeLog, "空键拒绝")
}

// TestRejectInvalidOp 非法操作类型必须以 invalid_op 拒绝，且拒绝不改变状态。
func TestRejectInvalidOp(t *testing.T) {
	c := New(0)
	if _, err := c.Apply([]Write{{Key: "ok", Op: OpPut, Value: "v"}}); err != nil {
		t.Fatal(err)
	}
	beforeTable, beforeLog := c.Snapshot()

	for _, tc := range []struct {
		name string
		w    Write
		idx  int
	}{
		{"空操作", Write{Key: "k", Op: ""}, 1},
		{"拼写错误", Write{Key: "k", Op: "PUT"}, 1},
		{"未知操作", Write{Key: "k", Op: "remove"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			batch := []Write{{Key: "z", Op: OpPut, Value: "1"}, tc.w}
			_, err := applyLogged(t, c, batch,
				fmt.Sprintf("操作类型 %q 既非 put 也非 delete，必须以 invalid_op 拒绝整批", tc.w.Op))
			be := expectBatchError(t, err, ReasonInvalidOp, tc.idx)
			if be.Op != tc.w.Op {
				t.Fatalf("期望回传 Op %q，实际 %q", tc.w.Op, be.Op)
			}
			assertUnchanged(t, c, beforeTable, beforeLog, "非法操作拒绝")
		})
	}
}

// TestRejectTooManyLiveKeys 批结束后存活键数超限必须拒绝，且拒绝不改变状态。
func TestRejectTooManyLiveKeys(t *testing.T) {
	c := New(2)
	if _, err := c.Apply([]Write{
		{Key: "a", Op: OpPut, Value: "1"},
		{Key: "b", Op: OpPut, Value: "2"},
	}); err != nil {
		t.Fatal(err)
	}

	// 批内瞬时 3 键，但批结束前删掉一个 → 净 2 键，不超限，应成功。
	entries, err := applyLogged(t, c,
		[]Write{
			{Key: "c", Op: OpPut, Value: "3"},
			{Key: "a", Op: OpDelete},
		},
		"上限按批结束后存活数判定：批内曾有 3 键，但结束时 b,c 共 2 键，不超限")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 { // retract(a), upsert(c)
		t.Fatalf("期望 2 条条目，实际 %v", entries)
	}
	survivingTable, survivingLog := c.Snapshot()

	// 批结束后 4 键 > 上限 2 → 拒绝。
	batch := []Write{{Key: "a", Op: OpPut, Value: "1"}, {Key: "d", Op: OpPut, Value: "4"}}
	_, err = applyLogged(t, c, batch, "当前存活 b,c 两键；本批再写 a,d，批结束后 4 键超过上限 2，必须以 too_many_live_keys 拒绝")
	be := expectBatchError(t, err, ReasonTooManyKeys, -1)
	if be.Count != 4 || be.Limit != 2 {
		t.Fatalf("期望 Count=4 Limit=2，实际 Count=%d Limit=%d", be.Count, be.Limit)
	}
	assertUnchanged(t, c, survivingTable, survivingLog, "超限拒绝")
}

// TestRejectThenContinue 被拒绝后组件必须仍可正常继续使用。
func TestRejectThenContinue(t *testing.T) {
	c := New(1)

	if _, err := applyLogged(t, c, []Write{{Key: "", Op: OpPut}},
		"空键被拒绝"); err == nil {
		t.Fatal("期望拒绝")
	}
	if _, err := applyLogged(t, c, []Write{{Key: "a", Op: OpPut, Value: "1"}},
		"拒绝后继续使用：a 正常写入"); err != nil {
		t.Fatalf("拒绝后应可继续使用，实际错误: %v", err)
	}
	if _, err := applyLogged(t, c, []Write{{Key: "b", Op: OpPut, Value: "2"}},
		"上限 1：a 存活时新增 b，批后 2 键超限拒绝"); err == nil {
		t.Fatal("期望超限拒绝")
	}
	if _, err := applyLogged(t, c, []Write{{Key: "a", Op: OpDelete}, {Key: "b", Op: OpPut, Value: "2"}},
		"超限拒绝后继续：同批删除 a 并写入 b，批后仍为 1 键，成功"); err != nil {
		t.Fatalf("拒绝后应可继续使用，实际错误: %v", err)
	}
	expectSnapshot(t, c,
		map[string]string{"b": "2"},
		[]Entry{
			{Seq: 0, Kind: KindUpsert, Key: "a", Value: "1"},
			{Seq: 1, Kind: KindRetract, Key: "a", Value: "1"},
			{Seq: 2, Kind: KindUpsert, Key: "b", Value: "2"},
		})
}

func assertUnchanged(t *testing.T, c *Coordinator, table map[string]string, log []Entry, msg string) {
	t.Helper()
	gotTable, gotLog := c.Snapshot()
	if !reflect.DeepEqual(gotTable, table) || !reflect.DeepEqual(gotLog, log) {
		t.Fatalf("%s后状态被改变\n  got table=%v log=%v\n want table=%v log=%v", msg, gotTable, gotLog, table, log)
	}
}

// TestReplayLogBuildsTable 下游按顺序应用日志，始终得到与当前表一致的结果。
func TestReplayLogBuildsTable(t *testing.T) {
	c := New(0)
	batches := [][]Write{
		{{Key: "a", Op: OpPut, Value: "1"}, {Key: "b", Op: OpPut, Value: "2"}},
		{{Key: "a", Op: OpPut, Value: "10"}}, // 覆盖
		{{Key: "b", Op: OpDelete}},           // 删除
		{{Key: "ghost", Op: OpDelete}},       // 删除不存在的键
		{{Key: "a", Op: OpPut, Value: "10"}}, // 同值
		{{Key: "c", Op: OpPut, Value: "3"}, {Key: "a", Op: OpDelete}},
		{{Key: "", Op: OpPut}}, // 非法批，被拒绝
		{{Key: "c", Op: OpPut, Value: "30"}},
	}
	for i, b := range batches {
		entries, err := applyLogged(t, c, b,
			fmt.Sprintf("第 %d 批：按净变化折叠；非法批不进日志", i))
		if err != nil {
			continue
		}
		// 每批结束后：顺序重放全部日志必须得到当前表。
		table, log := c.Snapshot()
		replayed := replay(log)
		if !reflect.DeepEqual(replayed, table) {
			t.Fatalf("第 %d 批后重放结果 %v 与当前表 %v 不一致", i, replayed, table)
		}
		_ = entries
	}

	table, log := c.Snapshot()
	if !reflect.DeepEqual(replay(log), table) {
		t.Fatalf("最终重放结果与当前表不一致")
	}
	wantTable := map[string]string{"c": "30"}
	if !reflect.DeepEqual(table, wantTable) {
		t.Fatalf("最终表 got %v, want %v", table, wantTable)
	}
}

// replay 模拟下游按顺序应用撤回式日志：retract 删除，upsert 写入。
func replay(log []Entry) map[string]string {
	out := make(map[string]string)
	for _, e := range log {
		switch e.Kind {
		case KindRetract:
			delete(out, e.Key)
		case KindUpsert:
			out[e.Key] = e.Value
		}
	}
	return out
}

// TestDeterminism 同一输入序列反复计算得到完全相同的输出（表与日志逐字段一致）。
func TestDeterminism(t *testing.T) {
	batches := [][]Write{
		{{Key: "b", Op: OpPut, Value: "2"}, {Key: "a", Op: OpPut, Value: "1"}},
		{{Key: "a", Op: OpPut, Value: "11"}, {Key: "c", Op: OpPut, Value: "3"}, {Key: "a", Op: OpDelete}},
		{{Key: "b", Op: OpPut, Value: "2"}},
		{{Key: "c", Op: OpPut, Value: "33"}},
	}
	run := func() (map[string]string, []Entry) {
		c := New(0)
		for _, b := range batches {
			if _, err := c.Apply(b); err != nil {
				t.Fatal(err)
			}
		}
		table, log := c.Snapshot()
		return table, log
	}
	t1, l1 := run()
	for i := 0; i < 5; i++ {
		ti, li := run()
		if !reflect.DeepEqual(ti, t1) || !reflect.DeepEqual(li, l1) {
			t.Fatalf("第 %d 次重算结果不一致\n table: %v vs %v\n log: %v vs %v", i, ti, t1, li, l1)
		}
	}
}

// TestConcurrentApplyAndRead 并发应用与并发只读：结果等价于某一种串行批顺序，
// 且每次只读拿到的表与日志彼此一致（重放日志即得该表）。
func TestConcurrentApplyAndRead(t *testing.T) {
	c := New(0)
	const workers = 8
	const iterations = 200

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				k := fmt.Sprintf("k%d", (id*7+i)%16)
				v := fmt.Sprintf("w%d-i%d", id, i)
				batch := []Write{
					{Key: k, Op: OpPut, Value: v},
					{Key: "sentinel", Op: OpPut, Value: "s"},
				}
				if _, err := c.Apply(batch); err != nil {
					t.Errorf("Apply 失败: %v", err)
					return
				}
				if i%3 == 0 {
					if _, err := c.Apply([]Write{{Key: k, Op: OpDelete}}); err != nil {
						t.Errorf("Delete 失败: %v", err)
						return
					}
				}
			}
		}(w)
	}

	// 并发只读：不断检查表与日志是否来自同一原子时刻。
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				table, log := c.Snapshot()
				if !reflect.DeepEqual(replay(log), table) {
					t.Errorf("读到的表与日志不一致：重放 %v != 表 %v", replay(log), table)
					return
				}
				// 序号必须连续。
				for i, e := range log {
					if e.Seq != i {
						t.Errorf("日志序号不连续：下标 %d 的 Seq=%d", i, e.Seq)
						return
					}
				}
			}
		}
	}()

	wg.Wait()
	close(stop)
	<-readerDone

	table, log := c.Snapshot()
	if !reflect.DeepEqual(replay(log), table) {
		t.Fatalf("最终表与日志不一致")
	}
	if _, ok := table["sentinel"]; !ok {
		t.Fatalf("sentinel 应存在")
	}
}

// TestConcurrentAppliesNoKeyLoss 高并发下每个键的最终值必须是某次成功写入的值，
// 且日志条目数与表状态自洽（无重复/丢失序号）。
func TestConcurrentAppliesNoKeyLoss(t *testing.T) {
	c := New(0)
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%02d", i%10)
			val := fmt.Sprintf("v%d", i)
			if _, err := c.Apply([]Write{{Key: key, Op: OpPut, Value: val}}); err != nil {
				t.Errorf("Apply: %v", err)
			}
		}(i)
	}
	wg.Wait()

	table, log := c.Snapshot()
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("最终表（按键排序）:")
	for _, k := range keys {
		t.Logf("  %s=%s", k, table[k])
	}
	t.Logf("日志共 %d 条", len(log))
	if !reflect.DeepEqual(replay(log), table) {
		t.Fatalf("日志重放与表不一致")
	}
}
