package ontology_test

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"ontology/ontology"
)

// entry 是测试中构造条目的简写。
func entry(rank int, key string, score ontology.Score) ontology.Entry {
	return ontology.Entry{Rank: rank, Key: key, Score: score}
}

// add / retract 是构造变更的简写。
func add(key string, score ontology.Score) ontology.Change {
	return ontology.Change{Kind: ontology.KindAdd, Row: ontology.Row{Key: key, Score: score}}
}

func retract(key string, score ontology.Score) ontology.Change {
	return ontology.Change{Kind: ontology.KindRetract, Row: ontology.Row{Key: key, Score: score}}
}

// applySeq 在一个 tracker 上按序应用变更，并逐条记录输入、判定依据与输出，
// 便于在测试日志中核对“输入 -> 判定 -> 输出”的完整链路。
func applySeq(t *testing.T, tr *ontology.TopNTracker, changes []ontology.Change) []ontology.ChangeResult {
	t.Helper()
	results := make([]ontology.ChangeResult, 0, len(changes))
	for i, c := range changes {
		res := tr.Apply(c)
		t.Logf("输入 #%d: kind=%d key=%q score=%d -> 接受=%v 原因=%d 离开=%v 进入=%v",
			i+1, c.Kind, c.Row.Key, c.Row.Score, res.Accepted, res.Reason, res.Left, res.Entered)
		results = append(results, res)
	}
	return results
}

// assertEntries 比较条目切片的 Rank/Key/Score 三字段。
func assertEntries(t *testing.T, name string, got, want []ontology.Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 长度不符 got=%v want=%v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d]: got=%+v want=%+v (全量 got=%v)", name, i, got[i], want[i], got)
		}
	}
}

// TestTieOrdering 验证分数并列时按键的字典序升序排列。
func TestTieOrdering(t *testing.T) {
	tr, err := ontology.NewTopNTracker(5, 0)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}

	// 故意打乱键的字典序与插入顺序；同分时键序与插入顺序无关。
	changes := []ontology.Change{
		add("banana", 90),
		add("apple", 90),
		add("cherry", 90),
		add("ab", 100),
		add("aa", 100),
	}
	applySeq(t, tr, changes)

	want := []ontology.Entry{
		entry(1, "aa", 100),
		entry(2, "ab", 100),
		entry(3, "apple", 90),
		entry(4, "banana", 90),
		entry(5, "cherry", 90),
	}
	top := tr.TopN()
	t.Logf("并列排序结果: %v", top)
	assertEntries(t, "topN", top, want)
}

// TestInsertEvictsTail 验证高分新行插入后，原榜尾离榜（进入/离开同时发生，离开在前）。
func TestInsertEvictsTail(t *testing.T) {
	tr, _ := ontology.NewTopNTracker(2, 0)
	results := applySeq(t, tr, []ontology.Change{
		add("a", 10),
		add("b", 20),
		add("c", 30), // 插入榜首，挤掉榜尾 a
	})

	// #3 前榜单: b(20,rank1) a(10,rank2)；加入 c(30) 后: c b，a 离开。
	r := results[2]
	if !r.Accepted {
		t.Fatalf("#3 应被接受")
	}
	assertEntries(t, "left", r.Left, []ontology.Entry{entry(2, "a", 10)})
	assertEntries(t, "entered", r.Entered, []ontology.Entry{entry(1, "c", 30)})
	assertEntries(t, "top", r.Top, []ontology.Entry{
		entry(1, "c", 30),
		entry(2, "b", 20),
	})
}

// TestRetractBackfill 验证撤回榜内行后由榜外排序最靠前的行补位；
// 榜尾撤回则无补位，撤回榜外行不影响榜单。
func TestRetractBackfill(t *testing.T) {
	tr, _ := ontology.NewTopNTracker(2, 0)
	results := applySeq(t, tr, []ontology.Change{
		add("a", 100),    // rank1
		add("b", 90),     // rank2
		add("c", 80),     // 榜外 rank3
		add("d", 95),     // 插入 rank2；榜单 a d，b 落到榜外
		retract("d", 95), // 撤回榜内 rank2 -> 榜外最强 b(90) 补位
		retract("c", 80), // 撤回榜外行 -> 榜单不变
	})

	// #5 撤回 d：离开 d(rank2)，进入 b(rank2)。
	r5 := results[4]
	assertEntries(t, "r5.left", r5.Left, []ontology.Entry{entry(2, "d", 95)})
	assertEntries(t, "r5.entered", r5.Entered, []ontology.Entry{entry(2, "b", 90)})
	assertEntries(t, "r5.top", r5.Top, []ontology.Entry{
		entry(1, "a", 100),
		entry(2, "b", 90),
	})

	// #6 撤回榜外的 c：榜单无变化，但变更仍被接受、记录日志。
	r6 := results[5]
	if !r6.Accepted || len(r6.Left) != 0 || len(r6.Entered) != 0 {
		t.Fatalf("撤回榜外行应被接受且榜单无变化, got=%+v", r6)
	}

	// 撤回榜首 a：离开 rank1，无补位（只剩 b 在榜）。
	r7 := tr.Apply(retract("a", 100))
	t.Logf("撤回榜首: 离开=%v 进入=%v top=%v", r7.Left, r7.Entered, r7.Top)
	assertEntries(t, "r7.left", r7.Left, []ontology.Entry{entry(1, "a", 100)})
	if len(r7.Entered) != 0 {
		t.Fatalf("撤回榜首且榜外无人时不应有进入, got=%v", r7.Entered)
	}
	assertEntries(t, "r7.top", r7.Top, []ontology.Entry{entry(1, "b", 90)})
}

// TestTieBackfillOrder 验证并列场景下补位者的选取：榜外分数最高、
// 分数相同时键字典序最靠前的行补位。
func TestTieBackfillOrder(t *testing.T) {
	tr, _ := ontology.NewTopNTracker(2, 0)
	applySeq(t, tr, []ontology.Change{
		add("top", 100), // rank1
		add("z", 80),    // rank2，在榜
		add("m", 50),    // 榜外
		add("a", 50),    // 榜外，与 m 同分但键更小
	})
	// 榜单: top(100) z(80)；榜外顺序 a(50) m(50)。
	r := tr.Apply(retract("z", 80))
	t.Logf("并列补位: 离开=%v 进入=%v", r.Left, r.Entered)
	assertEntries(t, "left", r.Left, []ontology.Entry{entry(2, "z", 80)})
	assertEntries(t, "entered", r.Entered, []ontology.Entry{entry(2, "a", 50)})
}

// TestRejections 覆盖全部拒绝原因，并验证被拒绝的输入不改变任何状态与日志。
func TestRejections(t *testing.T) {
	t.Run("非法构造参数", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			n, limit   int
			wantReason ontology.RejectReason
		}{
			{"n=0", 0, 0, ontology.RejectInvalidArgument},
			{"n=-1", -1, 0, ontology.RejectInvalidArgument},
			{"limit=-1", 3, -1, ontology.RejectInvalidArgument},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tr, err := ontology.NewTopNTracker(tc.n, tc.limit)
				if tr != nil || !errors.Is(err, ontology.ErrInvalidArgument) {
					t.Fatalf("got=(%v,%v), want nil+ErrInvalidArgument", tr, err)
				}
			})
		}
	})

	t.Run("运行期各类拒绝", func(t *testing.T) {
		tr, _ := ontology.NewTopNTracker(2, 3)
		applySeq(t, tr, []ontology.Change{
			add("a", 10),
			add("b", 20),
		})

		cases := []struct {
			name string
			c    ontology.Change
			want ontology.RejectReason
		}{
			{"未知变更种类", ontology.Change{Kind: ontology.KindUnknown, Row: ontology.Row{Key: "x"}}, ontology.RejectInvalidArgument},
			{"种类超出范围", ontology.Change{Kind: ontology.ChangeKind(99), Row: ontology.Row{Key: "x"}}, ontology.RejectInvalidArgument},
			{"空键", add("", 1), ontology.RejectInvalidArgument},
			{"撤回空键", retract("", 1), ontology.RejectInvalidArgument},
			{"新增已存在的键", add("a", 99), ontology.RejectDuplicateKey},
			{"撤回不存在的键", retract("zzz", 1), ontology.RejectKeyNotFound},
			{"撤回分数不符", retract("a", 11), ontology.RejectScoreMismatch},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				beforeTop := tr.TopN()
				beforeLog := tr.ChangeLog()
				beforeCount := tr.LiveCount()

				res := tr.Apply(tc.c)
				t.Logf("输入=%+v 判定=拒绝 原因=%d(%v)", tc.c, res.Reason, ontology.ReasonError(res.Reason))
				if res.Accepted || res.Reason != tc.want {
					t.Fatalf("got accepted=%v reason=%d, want reason=%d", res.Accepted, res.Reason, tc.want)
				}
				if !errors.Is(ontology.ReasonError(res.Reason), ontology.ReasonError(tc.want)) {
					t.Fatalf("ReasonError 映射不稳定")
				}
				// 状态与日志不得变化。
				assertEntries(t, "拒绝后 TopN", tr.TopN(), beforeTop)
				if tr.LiveCount() != beforeCount {
					t.Fatalf("拒绝后存活行数变化: %d -> %d", beforeCount, tr.LiveCount())
				}
				afterLog := tr.ChangeLog()
				if len(afterLog) != len(beforeLog) {
					t.Fatalf("拒绝后日志长度变化: %d -> %d", len(beforeLog), len(afterLog))
				}
			})
		}
	})

	t.Run("存活行数超限", func(t *testing.T) {
		tr, _ := ontology.NewTopNTracker(1, 2)
		applySeq(t, tr, []ontology.Change{add("a", 1), add("b", 2)})

		beforeLog := len(tr.ChangeLog())
		res := tr.Apply(add("c", 3))
		t.Logf("超限输入: 接受=%v 原因=%d(%v)", res.Accepted, res.Reason, ontology.ReasonError(res.Reason))
		if res.Accepted || res.Reason != ontology.RejectTooManyLiveRows {
			t.Fatalf("got accepted=%v reason=%d, want RejectTooManyLiveRows", res.Accepted, res.Reason)
		}
		if tr.LiveCount() != 2 || len(tr.ChangeLog()) != beforeLog {
			t.Fatalf("被拒绝的超限输入改变了状态: count=%d log=%d", tr.LiveCount(), len(tr.ChangeLog()))
		}

		// 撤回一行腾出名额后，同样的新增应被接受。
		if r := tr.Apply(retract("a", 1)); !r.Accepted {
			t.Fatalf("撤回应被接受")
		}
		if r := tr.Apply(add("c", 3)); !r.Accepted {
			t.Fatalf("腾出名额后新增应被接受, reason=%d", r.Reason)
		}
	})
}

// TestSnapshotConsistency 验证快照逐字段一致：Top 与 All 的前 N 项完全对应，
// 且快照是副本，外部修改不影响 tracker 内部状态。
func TestSnapshotConsistency(t *testing.T) {
	tr, _ := ontology.NewTopNTracker(2, 0)
	applySeq(t, tr, []ontology.Change{add("a", 10), add("b", 20), add("c", 30)})

	snap := tr.Snapshot()
	t.Logf("快照: top=%v all=%v live=%d", snap.Top, snap.All, snap.LiveCount)
	if snap.LiveCount != 3 || len(snap.All) != 3 || len(snap.Top) != 2 {
		t.Fatalf("快照字段不一致: %+v", snap)
	}
	for i := range snap.Top {
		if snap.Top[i] != snap.All[i] {
			t.Fatalf("Top[%d] 与 All[%d] 不一致: %+v vs %+v", i, i, snap.Top[i], snap.All[i])
		}
	}
	// Rank 必须连续从 1 开始。
	for i, e := range snap.All {
		if e.Rank != i+1 {
			t.Fatalf("Rank 不连续: %+v", snap.All)
		}
	}

	// 外部篡改返回切片后再次快照，内部状态应不受影响。
	snap.Top[0].Key = "HACKED"
	snap.All[0].Score = 999999
	snap2 := tr.Snapshot()
	if snap2.All[0].Key == "HACKED" || snap2.All[0].Score == 999999 {
		t.Fatalf("返回切片与内部状态共享底层数组: %+v", snap2)
	}
}

// TestDeterminism 验证同一输入序列反复计算得到完全相同的输出（含日志）。
func TestDeterminism(t *testing.T) {
	changes := []ontology.Change{
		add("q", 5), add("p", 5), add("r", 5),
		add("s", 9), retract("p", 5), add("p", 1),
		retract("s", 9), add("t", 7),
	}

	run := func() []ontology.LoggedChange {
		tr, _ := ontology.NewTopNTracker(3, 0)
		applySeq(t, tr, changes)
		return tr.ChangeLog()
	}

	first := run()
	for iter := 0; iter < 5; iter++ {
		got := run()
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", first) {
			t.Fatalf("第 %d 次运行输出不一致:\n%v\nvs\n%v", iter+1, got, first)
		}
	}
	t.Logf("5 次重复运行日志一致，共 %d 条", len(first))
}

// TestReplayLogReconstructsTopN 验证下游按日志顺序“先应用离开、再应用进入”
// 始终能重建正确的前 N 名集合与名次。Left/Entered 只传递成员变化，
// 留存行的名次变动由下游按同一排序规则（分数降序、键升序）重算。
func TestReplayLogReconstructsTopN(t *testing.T) {
	tr, _ := ontology.NewTopNTracker(3, 0)
	changes := []ontology.Change{
		add("a", 1), add("b", 2), add("c", 3), add("d", 4),
		retract("d", 4), retract("a", 1), add("e", 5),
		add("a", 1), retract("c", 3), add("f", 3),
	}
	applySeq(t, tr, changes)

	// 下游只维护存活于前 N 名的键 -> 分数；同一条日志内先删离开、再加进入。
	downstream := make(map[string]ontology.Score)
	for _, lc := range tr.ChangeLog() {
		for _, e := range lc.Left {
			if _, ok := downstream[e.Key]; !ok {
				t.Fatalf("seq=%d 日志要求离开一个下游本就没有的键 %q", lc.Seq, e.Key)
			}
			delete(downstream, e.Key)
		}
		for _, e := range lc.Entered {
			if _, ok := downstream[e.Key]; ok {
				t.Fatalf("seq=%d 日志要求进入一个下游已有的键 %q", lc.Seq, e.Key)
			}
			downstream[e.Key] = e.Score
		}

		// 按同一排序规则重算名次，与该日志的 TopAfter 逐字段比较。
		got := rankDownstream(downstream)
		t.Logf("seq=%d 重放后下游视图=%v TopAfter=%v", lc.Seq, got, lc.TopAfter)
		assertEntries(t, fmt.Sprintf("seq=%d", lc.Seq), got, lc.TopAfter)
	}

	// 终态与 tracker 当前前 N 名一致。
	assertEntries(t, "终态", rankDownstream(downstream), tr.TopN())
	t.Logf("重放 %d 条日志后终态一致: %v", len(tr.ChangeLog()), tr.TopN())
}

// rankDownstream 按分数降序、键字典序升序为下游集合重算名次。
func rankDownstream(m map[string]ontology.Score) []ontology.Entry {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]ontology.Entry, 0, len(keys))
	for i, k := range keys {
		out = append(out, ontology.Entry{Rank: i + 1, Key: k, Score: m[k]})
	}
	return out
}

// TestConcurrentReadWrite 在 -race 下验证并发写入与并发读取的安全性。
func TestConcurrentReadWrite(t *testing.T) {
	tr, _ := ontology.NewTopNTracker(5, 0)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 多个读者：持续取一致性快照并校验字段自洽。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snap := tr.Snapshot()
					if snap.LiveCount != len(snap.All) {
						panic(fmt.Sprintf("撕裂的快照: %+v", snap))
					}
					if len(snap.Top) > 5 || len(snap.Top) > len(snap.All) {
						panic(fmt.Sprintf("非法快照: %+v", snap))
					}
					_ = tr.ChangeLog()
				}
			}
		}()
	}

	// 一个写者：反复 add 再以一致分数 retract，穿插必然被拒绝的非法输入
	// （不存在的键、未知种类）；拒绝不得扰动状态，因此终态确定为空。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			key := fmt.Sprintf("k%d", i%20)
			tr.Apply(add(key, ontology.Score(i)))
			tr.Apply(retract(key, ontology.Score(i)))
			tr.Apply(retract("never-existed", 1))
			tr.Apply(ontology.Change{Kind: ontology.KindUnknown, Row: ontology.Row{Key: "x"}})
		}
		close(stop)
	}()

	wg.Wait()
	// 所有 add 的 key 都已配对 retract（dup 全部被拒），终态为空。
	if tr.LiveCount() != 0 {
		t.Fatalf("并发场景终态应为空, got=%d top=%v", tr.LiveCount(), tr.TopN())
	}
	t.Logf("并发读写完成，日志 %d 条，终态存活 %d", len(tr.ChangeLog()), tr.LiveCount())
}

// TestEmptyTracker 验证空 tracker 的初始读取行为。
func TestEmptyTracker(t *testing.T) {
	tr, _ := ontology.NewTopNTracker(3, 0)
	if got := tr.TopN(); len(got) != 0 {
		t.Fatalf("空 tracker TopN 应为空, got=%v", got)
	}
	snap := tr.Snapshot()
	if snap.LiveCount != 0 || len(snap.All) != 0 || len(snap.Top) != 0 {
		t.Fatalf("空快照非空: %+v", snap)
	}
	if len(tr.ChangeLog()) != 0 {
		t.Fatalf("空 tracker 不应有日志")
	}
}
