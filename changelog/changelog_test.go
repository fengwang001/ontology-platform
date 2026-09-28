package changelog_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/changelog"
)

// logBatch 打印一批输入、折叠输出条目与判定依据，
// 便于在 -v 输出中追踪每条日志的来由。
func logBatch(t *testing.T, label string, batch []changelog.Mutation, entries []changelog.Entry, err error) {
	t.Helper()
	t.Logf("=== %s ===", label)
	for i, m := range batch {
		t.Logf("  输入[%d] key=%q op=%s value=%q", i, m.Key, m.Op, m.Value)
	}
	if err != nil {
		t.Logf("  判定: 整批拒绝，原因=%v（表与日志不变）", err)
		return
	}
	if len(entries) == 0 {
		t.Logf("  判定: 各被触及键批前批后状态相同，净变化为空，不输出任何条目")
	}
	for i, e := range entries {
		t.Logf("  输出[%d] key=%q kind=%s value=%q", i, e.Key, e.Kind, e.Value)
	}
}

func TestApply_IntraBatchOverwrite(t *testing.T) {
	f := changelog.New(0)

	batch := []changelog.Mutation{
		{Key: "a", Op: changelog.OpPut, Value: "1"},
		{Key: "b", Op: changelog.OpPut, Value: "2"},
		{Key: "a", Op: changelog.OpPut, Value: "3"}, // 同批覆盖：净效果 a=3
		{Key: "a", Op: changelog.OpDelete},          // 同批删除：净效果 a 不存在
		{Key: "b", Op: changelog.OpPut, Value: "2"}, // 值未变：b 不输出
	}
	entries, err := f.Apply(batch)
	logBatch(t, "批内覆盖与同值重写", batch, entries, err)
	if err != nil {
		t.Fatalf("期望成功，得到错误 %v", err)
	}

	// a 经历 不存在->1->3->删除，净变化为“不存在”，故无条目；
	// b 写入 2 后又写 2，净变化为 upsert 2（首次使其存在）。
	expected := []changelog.Entry{
		{Key: "b", Kind: changelog.EntryUpsert, Value: "2"},
	}
	if !equalEntries(entries, expected) {
		t.Fatalf("条目不符：\n got=%v\nwant=%v", entries, expected)
	}
	if table := f.Table(); len(table) != 1 || table["b"] != "2" {
		t.Fatalf("当前表错误: %v", table)
	}
}

func TestApply_OverwriteExistingFoldsRetractThenUpsert(t *testing.T) {
	f := changelog.New(0)
	seed := []changelog.Mutation{{Key: "k", Op: changelog.OpPut, Value: "old"}}
	seedOut, err := f.Apply(seed)
	logBatch(t, "预置 k=old", seed, seedOut, err)
	if err != nil {
		t.Fatal(err)
	}

	batch := []changelog.Mutation{
		{Key: "k", Op: changelog.OpPut, Value: "mid"},
		{Key: "k", Op: changelog.OpPut, Value: "new"}, // 折叠后只看 old -> new
	}
	entries, err := f.Apply(batch)
	logBatch(t, "已存在键批内多次覆盖", batch, entries, err)
	if err != nil {
		t.Fatalf("期望成功，得到错误 %v", err)
	}
	expected := []changelog.Entry{
		{Key: "k", Kind: changelog.EntryRetract, Value: "old"},
		{Key: "k", Kind: changelog.EntryUpsert, Value: "new"},
	}
	if !equalEntries(entries, expected) {
		t.Fatalf("条目不符：\n got=%v\nwant=%v", entries, expected)
	}
}

func TestApply_DeleteNonexistentProducesNothing(t *testing.T) {
	f := changelog.New(0)
	batch := []changelog.Mutation{
		{Key: "ghost", Op: changelog.OpDelete}, // 删除本就不存在的键
	}
	entries, err := f.Apply(batch)
	logBatch(t, "删除不存在的键", batch, entries, err)
	if err != nil {
		t.Fatalf("删除不存在的键不应报错，得到 %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("期望无输出，得到 %v", entries)
	}
	if f.LiveCount() != 0 {
		t.Fatalf("期望存活键数为 0，得到 %d", f.LiveCount())
	}
}

func TestApply_EqualValueProducesNothing(t *testing.T) {
	f := changelog.New(0)
	seed := []changelog.Mutation{{Key: "same", Op: changelog.OpPut, Value: "v"}}
	if _, err := f.Apply(seed); err != nil {
		t.Fatal(err)
	}

	batch := []changelog.Mutation{
		{Key: "same", Op: changelog.OpPut, Value: "v"}, // 值相等，不输出
	}
	entries, err := f.Apply(batch)
	logBatch(t, "值相等不输出", batch, entries, err)
	if err != nil {
		t.Fatalf("期望成功，得到错误 %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("期望净变化为空，得到 %v", entries)
	}
}

func TestApply_DeleteExistingRetracts(t *testing.T) {
	f := changelog.New(0)
	if _, err := f.Apply([]changelog.Mutation{{Key: "x", Op: changelog.OpPut, Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	batch := []changelog.Mutation{{Key: "x", Op: changelog.OpDelete}}
	entries, err := f.Apply(batch)
	logBatch(t, "删除已存在键仅撤回", batch, entries, err)
	if err != nil {
		t.Fatal(err)
	}
	expected := []changelog.Entry{{Key: "x", Kind: changelog.EntryRetract, Value: "1"}}
	if !equalEntries(entries, expected) {
		t.Fatalf("条目不符：\n got=%v\nwant=%v", entries, expected)
	}
}

func TestApply_RecreateWithinBatchFoldsToUpsert(t *testing.T) {
	f := changelog.New(0)
	if _, err := f.Apply([]changelog.Mutation{{Key: "k", Op: changelog.OpPut, Value: "old"}}); err != nil {
		t.Fatal(err)
	}
	batch := []changelog.Mutation{
		{Key: "k", Op: changelog.OpDelete},
		{Key: "k", Op: changelog.OpPut, Value: "new"}, // 批内先删后建：old -> new
	}
	entries, err := f.Apply(batch)
	logBatch(t, "批内先删后建", batch, entries, err)
	if err != nil {
		t.Fatal(err)
	}
	expected := []changelog.Entry{
		{Key: "k", Kind: changelog.EntryRetract, Value: "old"},
		{Key: "k", Kind: changelog.EntryUpsert, Value: "new"},
	}
	if !equalEntries(entries, expected) {
		t.Fatalf("条目不符：\n got=%v\nwant=%v", entries, expected)
	}
}

func TestApply_OrderingByFirstAppearance(t *testing.T) {
	f := changelog.New(0)
	batch := []changelog.Mutation{
		{Key: "c", Op: changelog.OpPut, Value: "1"},
		{Key: "a", Op: changelog.OpPut, Value: "1"},
		{Key: "b", Op: changelog.OpPut, Value: "1"},
		{Key: "a", Op: changelog.OpPut, Value: "2"}, // 不影响键的先后
	}
	entries, err := f.Apply(batch)
	logBatch(t, "多键按首次出现顺序", batch, entries, err)
	if err != nil {
		t.Fatal(err)
	}
	gotKeys := make([]string, 0, len(entries))
	for _, e := range entries {
		gotKeys = append(gotKeys, e.Key)
	}
	wantKeys := []string{"c", "a", "b"}
	if fmt.Sprint(gotKeys) != fmt.Sprint(wantKeys) {
		t.Fatalf("顺序不符：got=%v want=%v", gotKeys, wantKeys)
	}
}

func TestApply_InvalidInputs(t *testing.T) {
	cases := []struct {
		name  string
		batch []changelog.Mutation
		want  error
	}{
		{
			name:  "空键",
			batch: []changelog.Mutation{{Key: "", Op: changelog.OpPut, Value: "v"}},
			want:  changelog.ErrEmptyKey,
		},
		{
			name: "空键出现在合法变更之后也整批拒绝",
			batch: []changelog.Mutation{
				{Key: "ok", Op: changelog.OpPut, Value: "v"},
				{Key: "", Op: changelog.OpDelete},
			},
			want: changelog.ErrEmptyKey,
		},
		{
			name:  "非法操作类型零值",
			batch: []changelog.Mutation{{Key: "k", Op: changelog.OpUnknown}},
			want:  changelog.ErrInvalidOp,
		},
		{
			name:  "非法操作类型越界值",
			batch: []changelog.Mutation{{Key: "k", Op: changelog.Op(99)}},
			want:  changelog.ErrInvalidOp,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := changelog.New(0)
			entries, err := f.Apply(tc.batch)
			logBatch(t, tc.name, tc.batch, entries, err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("期望错误 %v，得到 %v", tc.want, err)
			}
			if entries != nil {
				t.Fatalf("被拒绝批不应返回条目，得到 %v", entries)
			}
			if f.LiveCount() != 0 {
				t.Fatalf("被拒绝后表应保持为空，存活键数=%d", f.LiveCount())
			}
			if len(f.Log()) != 0 {
				t.Fatalf("被拒绝后日志应保持为空，得到 %v", f.Log())
			}

			// 拒绝后组件仍可继续使用。
			recover := []changelog.Mutation{{Key: "back", Op: changelog.OpPut, Value: "1"}}
			recEntries, recErr := f.Apply(recover)
			logBatch(t, tc.name+"-拒绝后继续写入", recover, recEntries, recErr)
			if recErr != nil || !equalEntries(recEntries,
				[]changelog.Entry{{Key: "back", Kind: changelog.EntryUpsert, Value: "1"}}) {
				t.Fatalf("拒绝后应可继续使用，entries=%v err=%v", recEntries, recErr)
			}
		})
	}
}

func TestApply_RejectedBatchLeavesStateUntouched(t *testing.T) {
	f := changelog.New(0)
	seed := []changelog.Mutation{{Key: "keep", Op: changelog.OpPut, Value: "v"}}
	if _, err := f.Apply(seed); err != nil {
		t.Fatal(err)
	}
	seedLog := len(f.Log())

	bad := []changelog.Mutation{
		{Key: "keep", Op: changelog.OpPut, Value: "changed"},
		{Key: "new", Op: changelog.OpPut, Value: "x"},
		{Key: "", Op: changelog.OpPut, Value: "illegal"},
	}
	entries, err := f.Apply(bad)
	logBatch(t, "非法批不得改动已有状态", bad, entries, err)
	if !errors.Is(err, changelog.ErrEmptyKey) {
		t.Fatalf("期望 ErrEmptyKey，得到 %v", err)
	}
	table, log := f.Snapshot()
	if table["keep"] != "v" {
		t.Fatalf("拒绝后 keep 应仍为 v，表=%v", table)
	}
	if _, ok := table["new"]; ok {
		t.Fatalf("拒绝后 new 不应存在，表=%v", table)
	}
	if len(log) != seedLog {
		t.Fatalf("拒绝后日志长度应不变：got=%d want=%d", len(log), seedLog)
	}
}

func TestApply_TooManyLiveKeys(t *testing.T) {
	f := changelog.New(2) // 至多 2 个存活键

	over := []changelog.Mutation{
		{Key: "a", Op: changelog.OpPut, Value: "1"},
		{Key: "b", Op: changelog.OpPut, Value: "1"},
		{Key: "c", Op: changelog.OpPut, Value: "1"},
	}
	entries, err := f.Apply(over)
	logBatch(t, "批结束后存活键数超限", over, entries, err)
	if !errors.Is(err, changelog.ErrTooManyLiveKeys) {
		t.Fatalf("期望 ErrTooManyLiveKeys，得到 %v", err)
	}
	if f.LiveCount() != 0 {
		t.Fatalf("超限批应整体回滚，存活键数=%d", f.LiveCount())
	}

	// 批内净增不超限则应成功（中间态不检查，只看批后状态）。
	ok := []changelog.Mutation{
		{Key: "a", Op: changelog.OpPut, Value: "1"},
		{Key: "a", Op: changelog.OpDelete},
		{Key: "b", Op: changelog.OpPut, Value: "1"},
	}
	okEntries, okErr := f.Apply(ok)
	logBatch(t, "中间超限但批后不超限", ok, okEntries, okErr)
	if okErr != nil {
		t.Fatalf("期望成功，得到 %v", okErr)
	}
	if f.LiveCount() != 1 {
		t.Fatalf("期望存活键数 1，得到 %d", f.LiveCount())
	}
	if !equalEntries(okEntries, []changelog.Entry{{Key: "b", Kind: changelog.EntryUpsert, Value: "1"}}) {
		t.Fatalf("条目不符：%v", okEntries)
	}

	// 删一建一保持数量不变，应成功。
	swap := []changelog.Mutation{
		{Key: "b", Op: changelog.OpDelete},
		{Key: "c", Op: changelog.OpPut, Value: "1"},
	}
	swapEntries, swapErr := f.Apply(swap)
	logBatch(t, "删一建一不超限", swap, swapEntries, swapErr)
	if swapErr != nil {
		t.Fatalf("期望成功，得到 %v", swapErr)
	}
	if !equalEntries(swapEntries, []changelog.Entry{
		{Key: "b", Kind: changelog.EntryRetract, Value: "1"},
		{Key: "c", Kind: changelog.EntryUpsert, Value: "1"},
	}) {
		t.Fatalf("条目不符：%v", swapEntries)
	}
}

func TestApply_ReplayLogRebuildsTable(t *testing.T) {
	// 端到端不变量：按顺序应用日志，始终能重建出当前表。
	f := changelog.New(0)
	batches := [][]changelog.Mutation{
		{{Key: "a", Op: changelog.OpPut, Value: "1"}, {Key: "b", Op: changelog.OpPut, Value: "2"}},
		{{Key: "a", Op: changelog.OpPut, Value: "10"}, {Key: "b", Op: changelog.OpDelete}},
		{{Key: "a", Op: changelog.OpDelete}, {Key: "c", Op: changelog.OpPut, Value: "3"}},
		{{Key: "c", Op: changelog.OpPut, Value: "3"}}, // 无净变化
		{{Key: "ghost", Op: changelog.OpDelete}},      // 无净变化
	}
	for i, b := range batches {
		entries, err := f.Apply(b)
		logBatch(t, fmt.Sprintf("重放序列第%d批", i), b, entries, err)
		if err != nil {
			t.Fatalf("第 %d 批错误: %v", i, err)
		}
		if err := replayConsistent(f.Log(), f.Table()); err != nil {
			t.Fatalf("第 %d 批后日志重放与当前表不一致: %v", i, err)
		}
	}
}

// replayConsistent 从空表开始顺序应用日志，校验结果与快照表逐字段一致。
func replayConsistent(log []changelog.Entry, table map[string]string) error {
	rebuilt := make(map[string]string)
	for _, e := range log {
		switch e.Kind {
		case changelog.EntryRetract:
			cur, ok := rebuilt[e.Key]
			if !ok {
				return fmt.Errorf("撤回 %q 时键不存在", e.Key)
			}
			if cur != e.Value {
				return fmt.Errorf("撤回 %q 值不符: 表中=%q 日志=%q", e.Key, cur, e.Value)
			}
			delete(rebuilt, e.Key)
		case changelog.EntryUpsert:
			rebuilt[e.Key] = e.Value
		default:
			return fmt.Errorf("未知条目种类 %d", e.Kind)
		}
	}
	if len(rebuilt) != len(table) {
		return fmt.Errorf("存活键数不符: 重放=%d 当前表=%d", len(rebuilt), len(table))
	}
	for k, v := range table {
		if rv, ok := rebuilt[k]; !ok || rv != v {
			return fmt.Errorf("键 %q 不符: 重放=%q 当前表=%q", k, rv, v)
		}
	}
	return nil
}

func TestApply_RepeatedSequenceIsDeterministic(t *testing.T) {
	seq := [][]changelog.Mutation{
		{{Key: "a", Op: changelog.OpPut, Value: "1"}, {Key: "b", Op: changelog.OpPut, Value: "2"}},
		{{Key: "b", Op: changelog.OpPut, Value: "20"}, {Key: "a", Op: changelog.OpDelete}},
		{{Key: "", Op: changelog.OpPut, Value: "x"}}, // 每轮都被拒绝
		{{Key: "c", Op: changelog.OpPut, Value: "3"}},
	}
	run := func() ([]changelog.Entry, map[string]string, []error) {
		f := changelog.New(0)
		var errs []error
		var all []changelog.Entry
		for _, b := range seq {
			e, err := f.Apply(b)
			errs = append(errs, err)
			all = append(all, e...)
		}
		table, log := f.Snapshot()
		if !equalEntries(all, log) {
			t.Fatalf("返回条目拼接与累计日志不一致")
		}
		return log, table, errs
	}

	log1, table1, errs1 := run()
	log2, table2, errs2 := run()
	t.Logf("两次重复计算：日志长度=%d 表大小=%d 错误数=%d", len(log1), len(table1), len(errs1))
	if !equalEntries(log1, log2) {
		t.Fatalf("日志不确定：\n%v\n%v", log1, log2)
	}
	if fmt.Sprint(table1) != fmt.Sprint(table2) {
		t.Fatalf("表不确定：\n%v\n%v", table1, table2)
	}
	for i := range errs1 {
		if !errors.Is(errs1[i], changelog.ErrEmptyKey) || !errors.Is(errs2[i], changelog.ErrEmptyKey) {
			if (errs1[i] == nil) != (errs2[i] == nil) {
				t.Fatalf("第 %d 批错误确定性不一致: %v vs %v", i, errs1[i], errs2[i])
			}
		}
	}
}

func TestApply_ConcurrentAtomicity(t *testing.T) {
	f := changelog.New(0)
	const goroutines = 16
	const rounds = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				key := fmt.Sprintf("g%d", g)
				batch := []changelog.Mutation{
					{Key: key, Op: changelog.OpPut, Value: fmt.Sprintf("%d-%d", g, r)},
				}
				if _, err := f.Apply(batch); err != nil {
					t.Errorf("Apply 返回错误: %v", err)
					return
				}
			}
		}(g)
	}

	// 并发只读：每次看到的表与日志必须彼此一致（日志可重放出该表）。
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				table, log := f.Snapshot()
				if err := replayConsistent(log, table); err != nil {
					t.Errorf("并发读到不一致快照: %v", err)
					return
				}
			}
		}
	}()

	wg.Wait()
	close(stop)
	<-readerDone

	table, log := f.Snapshot()
	if len(table) != goroutines {
		t.Fatalf("期望 %d 个存活键，得到 %d", goroutines, len(table))
	}
	if err := replayConsistent(log, table); err != nil {
		t.Fatalf("最终日志重放与表不一致: %v", err)
	}
	t.Logf("并发结束：存活键=%d，日志条目=%d，日志可顺序重放出当前表", len(table), len(log))
}

func TestSnapshot_DefensiveCopies(t *testing.T) {
	f := changelog.New(0)
	if _, err := f.Apply([]changelog.Mutation{{Key: "k", Op: changelog.OpPut, Value: "v"}}); err != nil {
		t.Fatal(err)
	}
	table := f.Table()
	table["k"] = "mutated"
	table["hacked"] = "1"
	log := f.Log()
	if len(log) > 0 {
		log[0].Value = "mutated"
	}
	if got := f.Table()["k"]; got != "v" {
		t.Fatalf("外部修改表拷贝泄漏进组件: %q", got)
	}
	if _, ok := f.Table()["hacked"]; ok {
		t.Fatal("外部新增键泄漏进组件")
	}
	if f.Log()[0].Value != "v" {
		t.Fatal("外部修改日志拷贝泄漏进组件")
	}
}

func equalEntries(a, b []changelog.Entry) bool {
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
