package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestRandomDifferential 大量随机「提交/回退/订正/审计故障」序列下，
// 优化重放器与朴素黄金模型在 StateAt 与 ReplayRange 上逐条对照。
func TestRandomDifferential(t *testing.T) {
	const seed = 20261007
	const instances = 5
	const iterations = 4000

	L := testLogger{t}
	rng := rand.New(rand.NewSource(seed))
	store := NewAuditStore()
	names := make([]string, instances)
	for i := range names {
		names[i] = fmt.Sprintf("I%d", i)
		store.Seed(names[i], "init")
	}
	exec, err := NewExecutor(store)
	if err != nil {
		t.Fatal(err)
	}

	// 记录可被订正的提交动作序号（非订正）。
	actionSeqs := []int{}
	actionWrites := map[int]map[string]string{}

	compare := func(tag string, step int) {
		t.Helper()
		n := store.Len()
		naive := NewNaiveModel(store)
		rp := NewReplayer(store)
		// 在多个时刻点对照状态。
		checkpoints := map[int]bool{0: true, n: true}
		for k := 0; k < 4 && n > 0; k++ {
			checkpoints[rng.Intn(n+1)] = true
		}
		for r := range checkpoints {
			got := rp.StateAt(r)
			want := naive.StateAt(r)
			if !mapsEqual(got, want) {
				t.Fatalf("[step=%d %s] StateAt(%d) 不一致\ngot =%s\nwant=%s",
					step, tag, r, stateString(got), stateString(want))
			}
		}
		// 随机区间对照事件。
		if n >= 2 {
			lo := rng.Intn(n)
			hi := lo + 1 + rng.Intn(n-lo)
			got, gerr := rp.ReplayRange(lo, hi)
			if gerr != nil {
				t.Fatalf("ReplayRange err: %v", gerr)
			}
			want := naive.ReplayRange(lo, hi)
			if !sameEvents(got, want) {
				t.Fatalf("[step=%d %s] ReplayRange(%d,%d] 不一致\ngot =%s\nwant=%s",
					step, tag, lo, hi, eventsString(got), eventsString(want))
			}
		}
	}

	for step := 1; step <= iterations; step++ {
		roll := rng.Float64()
		switch {
		case roll < 0.12 && len(actionSeqs) > 0:
			// 审计写入故障（对动作或订正）。
			store.InjectAppendFailures(1)
			if rng.Intn(2) == 0 {
				writes := randomWrites(rng, names)
				_, err := exec.ExecuteAction(fmt.Sprintf("A%d", step), writes, rng.Intn(3) == 0)
				if !IsAuditWriteFailure(err) {
					t.Fatalf("step=%d 期望审计失败，得到 %v", step, err)
				}
				if step <= 60 {
					L.log("step=%d 输入=故障注入+动作 err=%v 依据=Len 未增、状态回退", step, err)
				}
			}
			store.ResetFaults()

		case roll < 0.30 && len(actionSeqs) > 0:
			// 订正：目标可能合法 / 非法 / 指向订正，用于覆盖参数分支。
			target := actionSeqs[rng.Intn(len(actionSeqs))]
			corrected := map[string]string{}
			for inst := range actionWrites[target] {
				if rng.Intn(2) == 0 {
					corrected[inst] = fmt.Sprintf("c%d-%d", step, rng.Intn(999))
				}
			}
			if len(corrected) == 0 {
				corrected[fmt.Sprintf("I%d", rng.Intn(instances))] = fmt.Sprintf("c%d", step)
			}
			rec, err := exec.Correct(fmt.Sprintf("C%d", step), target, corrected)
			if err != nil {
				if !IsInvalidInput(err) {
					t.Fatalf("step=%d 订正意外错误 %v", step, err)
				}
			} else {
				_ = rec
				if step <= 60 {
					L.log("step=%d 输入=订正 seq%d -> %v 输出=seq%d 依据=订正占用序号且原值保留",
						step, target, corrected, rec.Seq)
				}
			}

		default:
			// 普通动作：提交或业务回退。
			writes := randomWrites(rng, names)
			rollback := rng.Float64() < 0.25
			rec, err := exec.ExecuteAction(fmt.Sprintf("A%d", step), writes, rollback)
			if err != nil {
				if !IsInvalidInput(err) && !IsAuditWriteFailure(err) {
					t.Fatalf("step=%d 意外错误 %v", step, err)
				}
				continue
			}
			if !rollback {
				actionSeqs = append(actionSeqs, rec.Seq)
				actionWrites[rec.Seq] = writes
			}
			if step <= 60 {
				L.log("step=%d 输入=动作 rollback=%v writes=%v 输出=seq%d/%s 依据=%s",
					step, rollback, writes, rec.Seq, rec.Outcome,
					ternary(rollback, "回退 Before==After 且不占 latest", "提交生效"))
			}
		}

		if step%127 == 0 || step == iterations {
			compare("periodic", step)
		}
	}

	// 最终全量对照：每个序号都对齐，且哈希链完整。
	naive := NewNaiveModel(store)
	rp := NewReplayer(store)
	n := store.Len()
	for r := 0; r <= n; r++ {
		if !mapsEqual(rp.StateAt(r), naive.StateAt(r)) {
			t.Fatalf("最终对照 StateAt(%d) 不一致", r)
		}
	}
	if err := store.verifyHashChain(); err != nil {
		t.Fatalf("哈希链损坏: %v", err)
	}
	L.log("依据: 随机 %d 步、最终 %d 条记录，全部时刻 StateAt 与随机区间 ReplayRange 与朴素模型一致；哈希链完整",
		iterations, n)
}

func randomWrites(rng *rand.Rand, names []string) map[string]string {
	writes := map[string]string{}
	k := 1 + rng.Intn(len(names))
	perm := rng.Perm(len(names))
	for i := 0; i < k; i++ {
		writes[names[perm[i]]] = fmt.Sprintf("v%04d", rng.Intn(10000))
	}
	return writes
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
