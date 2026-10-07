package audit

import (
	"fmt"
	"testing"
)

// TestReplayCostIndependentOfHistoryLength 以可验证方式证明：
// 区间重放读取的审计记录条数只取决于区间长度与快照间隔，
// 不随该类型历史审计记录总数增长。
//
// 方法：固定一个很短的目标区间，分别在历史长度为 N 与 2N 时
// 重置日志读取计数器后执行重放，比较读取条数。若开销与历史
// 总长无关，二者应几乎相同（≤ 2*interval + 区间长度）。
func TestReplayCostIndependentOfHistoryLength(t *testing.T) {
	tl := &testLogger{}
	defer tl.flush(t)

	_, log, exec, replayer, naive := newSystem(50)
	const typ = "Big"
	mustRegister(t, exec, typ, "a", "0")

	appendN := func(n int) {
		base := log.LastSeq(typ)
		for i := int64(0); i < int64(n); i++ {
			if _, err := exec.Execute(Action{
				ActionID: fmt.Sprintf("h%d", base+i),
				TypeName: typ,
				Writes:   []Write{{"a", fmt.Sprintf("v%d", base+i)}},
			}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}

	// fastReads 统计快速重放器在固定长度 10 的尾部区间上读取的记录数。
	fastReads := func() int {
		last := log.LastSeq(typ)
		log.ResetReadCounters()
		res, err := replayer.Replay(typ, last-10, last) // 固定区间长度 10
		if err != nil {
			t.Fatal(err)
		}
		if len(res.StateTo) == 0 {
			t.Fatalf("empty state")
		}
		return log.RangeReadCount()
	}

	// naiveReads 统计朴素模型在同区间上的读取数（它永远从 1 扫到 to）。
	naiveReads := func() int {
		last := log.LastSeq(typ)
		log.ResetReadCounters()
		if _, err := naive.Replay(typ, last-10, last); err != nil {
			t.Fatal(err)
		}
		return log.RangeReadCount()
	}

	appendN(500)
	costAtN := fastReads()
	naiveN := naiveReads()
	tl.logf("历史长度 500：尾部长度 10 区间重放读取记录数 = %d", costAtN)
	tl.logf("朴素模型同一区间从头扫描读取记录数 = %d", naiveN)

	appendN(500) // 总历史 1000
	costAt2N := fastReads()
	naive2N := naiveReads()
	tl.logf("历史长度 1000：快速重放读取 = %d；朴素扫描读取 = %d", costAt2N, naive2N)

	bound := 2*replayer.SnapshotInterval() + 12
	if int64(costAtN) > bound || int64(costAt2N) > bound {
		t.Fatalf("replay read cost %d/%d exceeds bound %d (depends on history length?)",
			costAtN, costAt2N, bound)
	}
	if costAt2N > costAtN+2 {
		t.Fatalf("cost grew with history: %d -> %d", costAtN, costAt2N)
	}
	if naive2N <= naiveN {
		t.Fatalf("naive cost should grow with history: %d -> %d", naiveN, naive2N)
	}
	tl.logf("依据: 快速重放两次均 ≤ %d 且基本相等；朴素模型 %d→%d 随历史线性增长",
		bound, naiveN, naive2N)
}
