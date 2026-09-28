package changelog

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func entriesString(es []Entry) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, e := range es {
		if i > 0 {
			b.WriteByte(' ')
		}
		switch e.Kind {
		case EntryRetract:
			fmt.Fprintf(&b, "retract(%s=%s)", e.Key, e.Value)
		case EntryInsert:
			fmt.Fprintf(&b, "insert(%s=%s)", e.Key, e.Value)
		}
	}
	b.WriteByte(']')
	return b.String()
}

func writesString(ws []Write) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, w := range ws {
		if i > 0 {
			b.WriteByte(' '
		}
		switch w.Op {
		case OpPut:
			fmt.Fprintf(&b, "put(%s=%s)", w.Key, w.Value)
		case OpDelete:
			fmt.Fprintf(&b, "delete(%s)", w.Key)
		default:
			fmt.Fprintf(&b, "op%d(%s=%s)", int(w.Op), w.Key, w.Value)
		}
	}
	b.WriteByte(']')
	return b.String()
}

// applyAndLog 应用一批，并在测试日志中打印输入、输出条目与判定依据。
func applyAndLog(t *testing.T, f *Folder, batch []Write, why string) []Entry {
	t.Helper()
	t.Logf("输入批: %s | 判定依据: %s", writesString(batch), why)
	out, err := f.Apply(batch)
	if err != nil {
		t.Logf("  -> 拒绝: %v", err)
	} else {
		t.Logf("  -> 输出条目: %s", entriesString(out))
	}
	return out
}

// replay 按顺序把日志条目应用到空表，返回重建出的当前表。
func replay(log []Entry) map[string]string {
	m := make(map[string]string)
	for _, e := range log {
		switch e.Kind {
		case EntryRetract:
			delete(m, e.Key)
		case EntryInsert:
			m[e.Key] = e.Value
		}
	}
	return m
}

func TestBasicInsertThenRetractOrder(t *testing.T) {
	f, _ := NewFolder(10)

	// 空表上写入两个新键：只输出 insert，且按首次出现顺序 a, b。
	out := applyAndLog(t, f, []Write{
		{OpPut, "a", "1"},
		{OpPut, "b", "2"},
	}, "键此前不存在 -> 各产生一条 insert，按首次出现顺序排列")
	want := []Entry{
		{EntryInsert, "a", "1"},
		{EntryInsert, "b", "2"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}

	// a 改值、b 删除：必须先 retract 旧值再 insert 新值；删除只 retract。
	out = applyAndLog(t, f, []Write{
		{OpPut, "a", "9"},
		{OpDelete, "b", ""},
	}, "a 值变化 -> retract 旧值 + insert 新值；b 删除 -> 仅 retract")
	want = []Entry{
		{EntryRetract, "a", "1"},
		{EntryInsert, "a", "9"},
		{EntryRetract, "b", "2"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}

	// 按顺序回放全部日志，必须得到当前表。
	table, log := f.Snapshot()
	if got := replay(log); !reflect.DeepEqual(got, table) {
		t.Fatalf("replay %v != table %v", got, table)
	}
	if !reflect.DeepEqual(table, map[string]string{"a": "9"}) {
		t.Fatalf("unexpected table: %v", table)
	}
}

func TestIntraBatchOverwrite(t *testing.T) {
	f, _ := NewFolder(10)

	// 批内对同一键多次覆盖：只看批前（不存在）与批后（c=3），净效果一条 insert。
	out := applyAndLog(t, f, []Write{
		{OpPut, "c", "1"},
		{OpPut, "c", "2"},
		{OpPut, "c", "3"},
	}, "同一键批内被多次 put，只折叠批前/批后净变化 -> 单条 insert(c=3)")
	want := []Entry{{EntryInsert, "c", "3"}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}

	// 批内 put 后又 delete：净效果为不存在 -> 无输出。
	out = applyAndLog(t, f, []Write{
		{OpPut, "d", "x"},
		{OpDelete, "d", ""},
	}, "d 批内 put 后 delete，批前后都不存在 -> 状态相同，不输出")
	if len(out) != 0 {
		t.Fatalf("got %v, want empty", out)
	}

	// 批内 delete 后又 put 回原值：净状态与批前相同 -> 无输出。
	out = applyAndLog(t, f, []Write{
		{OpDelete, "c", ""},
		{OpPut, "c", "3"},
	}, "c 批内先 delete 再以相同值 put 回，净状态不变 -> 不输出")
	if len(out) != 0 {
		t.Fatalf("got %v, want empty", out)
	}

	// 批内 delete 后 put 不同值：净效果为改值 -> retract + insert。
	out = applyAndLog(t, f, []Write{
		{OpDelete, "c", ""},
		{OpPut, "c", "4"},
	}, "c 批内 delete 后以不同值 put 回 -> retract(3) + insert(4)")
	want = []Entry{
		{EntryRetract, "c", "3"},
		{EntryInsert, "c", "4"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}

	// 多键交织，顺序按各键首次出现：e 在 c 之前首次出现。
	out = applyAndLog(t, f, []Write{
		{OpPut, "e", "1"},
		{OpPut, "c", "5"},
		{OpPut, "e", "2"},
	}, "首次出现顺序 e 早于 c；e 净变化 1->2 折叠为单条 insert，c 4->5 retract+insert")
	want = []Entry{
		{EntryInsert, "e", "2"},
		{EntryRetract, "c", "4"},
		{EntryInsert, "c", "5"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func TestDeleteNonExistentKey(t *testing.T) {
	f, _ := NewFolder(10)

	// 删除不存在的键：批前后都不存在 -> 不输出，也不报错。
	out := applyAndLog(t, f, []Write{{OpDelete, "ghost", ""}},
		"删除批前不存在的键，且批内未重新写入 -> 状态相同，不输出")
	if len(out) != 0 {
		t.Fatalf("got %v, want empty", out)
	}
	if f.LiveKeyCount() != 0 {
		t.Fatalf("table should stay empty, got %v", f.Table())
	}
}

func TestEqualValueNoOutput(t *testing.T) {
	f, _ := NewFolder(10)
	applyAndLog(t, f, []Write{{OpPut, "k", "v"}}, "准备初始状态 k=v")

	// 以相等的值重复写入：存在且值相等 -> 不输出。
	out := applyAndLog(t, f, []Write{{OpPut, "k", "v"}},
		"批前后 k 都存在且值相等 -> 不输出")
	if len(out) != 0 {
		t.Fatalf("got %v, want empty", out)
	}

	// 批内多次写入但最终值等于原值：仍不输出。
	out = applyAndLog(t, f, []Write{
		{OpPut, "k", "other"},
		{OpPut, "k", "v"},
	}, "批内一度改成 other，但批结束时仍为原值 v -> 净状态相同，不输出")
	if len(out) != 0 {
		t.Fatalf("got %v, want empty", out)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name   string
		limit  int
		batch  []Write
		reason RejectReason
	}{
		{
			name:   "empty key on put",
			limit:  10,
			batch:  []Write{{OpPut, "", "v"}},
			reason: RejectEmptyKey,
		},
		{
			name:   "empty key on delete",
			limit:  10,
			batch:  []Write{{OpDelete, "", ""}},
			reason: RejectEmptyKey,
		},
		{
			name:   "empty key after a valid write",
			limit:  10,
			batch:  []Write{{OpPut, "a", "1"}, {OpPut, "", "2"}},
			reason: RejectEmptyKey,
		},
		{
			name:   "invalid op zero",
			limit:  10,
			batch:  []Write{{Op(0), "a", "1"}},
			reason: RejectInvalidOp,
		},
		{
			name:   "invalid op out of range",
			limit:  10,
			batch:  []Write{{Op(99), "a", "1"}},
			reason: RejectInvalidOp,
		},
		{
			name:   "too many live keys",
			limit:  2,
			batch:  []Write{{OpPut, "a", "1"}, {OpPut, "b", "2"}, {OpPut, "c", "3"}},
			reason: RejectTooManyLiveKeys,
		},
		{
			name:   "limit exceeded only net: deleted key must not count",
			limit:  2,
			batch:  []Write{{OpPut, "a", "1"}, {OpPut, "b", "2"}, {OpDelete, "b", ""}, {OpPut, "c", "3"}},
			reason: RejectTooManyLiveKeys, // a 与 c 存活 = 2，未超限；改成会超限的序列
		},
	}
	// 修正最后一个用例：上限 2，最终 a,b,c 同时存活 -> 超限；中间删除不影响判定依据。
	cases[len(cases)-1].batch = []Write{
		{OpPut, "a", "1"}, {OpPut, "b", "2"},
		{OpPut, "x", "tmp"}, {OpDelete, "x", ""}, // 批内瞬态键不计入
		{OpPut, "c", "3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := NewFolder(tc.limit)
			out := applyAndLog(t, f, tc.batch,
				fmt.Sprintf("非法批，期望拒绝原因: %s", tc.reason))
			if out != nil {
				t.Fatalf("expected nil output, got %v", out)
			}
			var re *RejectError
			_, err := f.Apply(tc.batch)
			if err == nil {
				t.Fatal("expected rejection error, got nil")
			}
			if !AsRejectError(err, &re) {
				t.Fatalf("expected *RejectError, got %T: %v", err, err)
			}
			if re.Reason != tc.reason {
				t.Fatalf("reason = %s, want %s", re.Reason, tc.reason)
			}
			t.Logf("  可区分原因确认: %s (index=%d, detail=%q)", re.Reason, re.Index, re.Detail)

			// 被拒绝后状态为空，且仍可继续使用。
			if f.LiveKeyCount() != 0 || len(f.Log()) != 0 {
				t.Fatalf("rejected batch must not mutate state: %v / %v", f.Table(), f.Log())
			}
			good := []Write{{OpPut, "ok", "1"}}
			goodOut, err := f.Apply(good)
			if err != nil || !reflect.DeepEqual(goodOut, []Entry{{EntryInsert, "ok", "1"}}) {
				t.Fatalf("folder not reusable after rejection: %v %v", goodOut, err)
			}
		})
	}
}

func TestRejectedBatchKeepsPriorState(t *testing.T) {
	f, _ := NewFolder(2)
	applyAndLog(t, f, []Write{{OpPut, "a", "1"}}, "先建立既有状态 a=1 与一条日志")

	beforeTable, beforeLog := f.Snapshot()
	bad := []Write{{OpPut, "b", "2"}, {OpPut, "c", "3"}} // 上限 2 -> 3 个存活键，超限
	applyAndLog(t, f, bad, "超限批必须整体拒绝，既有表与日志保持不变")

	afterTable, afterLog := f.Snapshot()
	if !reflect.DeepEqual(beforeTable, afterTable) {
		t.Fatalf("table changed: before=%v after=%v", beforeTable, afterTable)
	}
	if !reflect.DeepEqual(beforeLog, afterLog) {
		t.Fatalf("log changed: before=%v after=%v", beforeLog, afterLog)
	}
}

func TestLimitAllowsReplacementAtCapacity(t *testing.T) {
	f, _ := NewFolder(2)
	applyAndLog(t, f, []Write{{OpPut, "a", "1"}, {OpPut, "b", "2"}}, "达到上限 2")

	// 已满时改值不新增存活键 -> 允许。
	out := applyAndLog(t, f, []Write{{OpPut, "a", "11"}}, "容量已满但只改值，存活键数不变 -> 允许")
	if len(out) != 2 {
		t.Fatalf("got %v, want retract+insert", out)
	}
	// 已满时先删一个再加一个 -> 允许。
	out = applyAndLog(t, f, []Write{{OpDelete, "b"}, {OpPut, "c", "3"}}, "删 b 增 c，存活键数保持 2 -> 允许")
	want := []Entry{{EntryRetract, "b", "2"}, {EntryInsert, "c", "3"}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func TestDeterminism(t *testing.T) {
	sequence := [][]Write{
		{{OpPut, "a", "1"}, {OpPut, "b", "2"}},
		{{OpPut, "a", "11"}, {OpDelete, "b", ""}, {OpPut, "c", "3"}},
		{{OpPut, "a", "11"}, {OpDelete, "ghost", ""}}, // 相等值 + 删除不存在键 -> 空输出
		{{OpDelete, "c", ""}},
	}
	f1, _ := NewFolder(10)
	f2, _ := NewFolder(10)
	for i, batch := range sequence {
		o1, e1 := f1.Apply(batch)
		o2, e2 := f2.Apply(batch)
		if !reflect.DeepEqual(o1, o2) || !reflect.DeepEqual(e1, e2) {
			t.Fatalf("batch %d output differs", i)
		}
	}
	t1, l1 := f1.Snapshot()
	t2, l2 := f2.Snapshot()
	if !reflect.DeepEqual(t1, t2) || !reflect.DeepEqual(l1, l2) {
		t.Fatalf("same input sequence produced different results: %v/%v vs %v/%v", t1, l1, t2, l2)
	}
	t.Logf("确定性确认: 两次独立计算得到相同日志 %s 与表 %v", entriesString(l1), t1)

	// 不变式：任意时刻按序回放日志都等于当前表。
	if got := replay(l1); !reflect.DeepEqual(got, t1) {
		t.Fatalf("replay %v != table %v", got, t1)
	}
}

func TestConcurrentApplyAndReads(t *testing.T) {
	f, _ := NewFolder(4)

	// 每个 writer 反复在「写自己的键」和「删除自己的键」之间切换；
	// 任意已提交状态都必须满足：存活键数 <= 上限、日志可回放为当前表。
	const writers = 8
	const rounds = 200
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		key := fmt.Sprintf("k%d", g)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				if r%2 == 0 {
					if _, err := f.Apply([]Write{{OpPut, key, fmt.Sprintf("v%d", r)}}); err != nil {
						t.Errorf("put failed: %v", err)
						return
					}
				} else {
					if _, err := f.Apply([]Write{{OpDelete, key, ""}}); err != nil {
						t.Errorf("delete failed: %v", err)
						return
					}
				}
			}
		}()
	}

	// 并发读者：每一次读到的表与日志都必须逐字段自洽。
	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	for r := 0; r < 4; r++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				table, log := f.Snapshot()
				if len(table) > 4 {
					t.Errorf("live keys %d exceed limit", len(table))
					return
				}
				if got := replay(log); !reflect.DeepEqual(got, table) {
					t.Errorf("concurrent read inconsistent: replay %v != table %v", got, table)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	readerWg.Wait()

	table, log := f.Snapshot()
	if got := replay(log); !reflect.DeepEqual(got, table) {
		t.Fatalf("final replay %v != table %v", got, table)
	}
	t.Logf("并发结束: 存活键=%d, 日志条目=%d, 表=%v", len(table), len(log), table)
}

func TestNewFolderRejectsNonPositiveLimit(t *testing.T) {
	for _, n := range []int{0, -1} {
		if _, err := NewFolder(n); err != ErrInvalidLimit {
			t.Fatalf("NewFolder(%d) err = %v, want ErrInvalidLimit", n, err)
		}
	}
}
