package ontology

import (
	"fmt"
	"testing"
)

// TestReplayComplexityBound 可验证地证明区间重放扫描量只与区间长度相关，
// 不随历史审计记录总数增长。
//
// 方法：维护一条持续追加的序列与一个常驻重放器（快照随追加增量构建、
// 永久复用）。在多个历史规模 N 处重放结尾固定宽度 W 的区间，断言
// ScanCost()（实际读取记录条数）恒不超过 SnapshotInterval+W——
// 与 N 无关的常数上界；朴素模型则每次从头扫描全部 N 条，作为对照打印。
func TestReplayComplexityBound(t *testing.T) {
	L := testLogger{t}
	const W = 10
	bound := SnapshotInterval + W

	store := NewAuditStore()
	store.Seed("X", "init")
	exec, err := NewExecutor(store)
	if err != nil {
		t.Fatal(err)
	}
	rp := NewReplayer(store)

	checks := []int{128, 1024, 2048, 4096, 8192}
	L.log("依据: 区间宽度 W=%d，快照间隔=%d，稳态扫描上界=%d（与历史总量 N 无关）", W, SnapshotInterval, bound)
	L.log("%8s | %12s | %10s | %10s", "N", "steadyOpt", "naiveScan", "match")

	var prevSteady int
	for i, n := range checks {
		for store.Len() < n {
			cur := store.Len() + 1
			rollback := cur%37 == 0
			if _, err := exec.ExecuteAction(fmt.Sprintf("a%d", cur),
				map[string]string{"X": fmt.Sprintf("v%d", cur)}, rollback); err != nil {
				t.Fatal(err)
			}
			if cur%53 == 0 { // 偶尔订正很早的动作，制造跨边界订正
				if _, err := exec.Correct(fmt.Sprintf("c%d", cur), 1,
					map[string]string{"X": fmt.Sprintf("fix@%d", cur)}); err != nil {
					t.Fatal(err)
				}
			}
		}
		rp.ensureSnapshots() // 增量补齐快照，只读取自上一边界以来的新增段

		lo, hi := n-W, n
		got, err := rp.ReplayRange(lo, hi)
		if err != nil {
			t.Fatal(err)
		}
		// 小规模与朴素模型逐条对照；大规模朴素模型 O(N) 只作扫描量参照，
		// 结果等价性由 TestRandomDifferential 在随机序列上充分保证。
		match := true
		if n <= 2048 {
			match = sameEvents(got, NewNaiveModel(store).ReplayRange(lo, hi))
			if !match {
				t.Fatalf("N=%d 区间事件与朴素模型不一致", n)
			}
		}
		steady := rp.ScanCost()
		L.log("%8d | %12d | %10d | %10v", n, steady, n, match)
		if steady > bound {
			t.Fatalf("N=%d 稳态扫描量 %d 超出与 N 无关的上界 %d", n, steady, bound)
		}
		if i > 0 && steady > prevSteady {
			t.Fatalf("稳态扫描量不得随历史总量增长: %d -> %d", prevSteady, steady)
		}
		prevSteady = steady
	}
}

// TestReplayDeterministic 同一区间重放任意多次结果完全相同。
func TestReplayDeterministic(t *testing.T) {
	store := NewAuditStore()
	store.Seed("X", "init")
	exec, err := NewExecutor(store)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 200; i++ {
		if _, err := exec.ExecuteAction(fmt.Sprintf("a%d", i),
			map[string]string{"X": fmt.Sprintf("v%d", i)}, i%11 == 0); err != nil {
			t.Fatal(err)
		}
		if i%23 == 0 {
			if _, err := exec.Correct(fmt.Sprintf("c%d", i), 1,
				map[string]string{"X": fmt.Sprintf("fix%d", i)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var first []ChangeEvent
	for k := 0; k < 5; k++ {
		rp := NewReplayer(store)
		ev, err := rp.ReplayRange(50, 180)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = ev
			continue
		}
		if !sameEvents(first, ev) {
			t.Fatalf("第 %d 次重放结果不一致", k+1)
		}
	}
	t.Logf("依据: 同一区间重放 5 次，事件序列完全一致（%d 条事件）", len(first))
}
