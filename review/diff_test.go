package review

import (
	"fmt"
	"math/rand"
	"testing"
)

// genScenario 构造一条随机但结构受约束的操作序列：
// 先登记若干评委/申报人，再在其之上随机执行建评审、投票、
// 回避、异议、裁定、终局等操作，使各分支与边界都有机会被覆盖。
func genScenario(rng *rand.Rand) []Op {
	var ops []Op
	nRev := 7 + rng.Intn(6) // 7..12 名评委
	nApp := 1 + rng.Intn(3) // 1..3 名申报人
	for i := 1; i <= nRev; i++ {
		unit := 1 + rng.Intn(4) // U1..U4
		group := 1 + rng.Intn(2)
		ops = append(ops, Op{"addReviewer", []int{i, unit, group}})
	}
	for i := 1; i <= nApp; i++ {
		appID := 100 + i
		unit := 1 + rng.Intn(5) // U1..U5（可能制造同单位回避）
		ops = append(ops, Op{"addApplicant", []int{appID, unit}})
	}

	// 预登记少量关系/回避申请（可能命中未来评委）。
	for i := 0; i < rng.Intn(3); i++ {
		app := 101 + rng.Intn(nApp)
		rev := 1 + rng.Intn(nRev)
		ops = append(ops, Op{pick(rng, "relation", "recusalRequest"), []int{app, rev}})
	}

	steps := 120 + rng.Intn(200)
	// 预决定建评审的位置并预分配 ID（两侧 create 成功 ID 均从 1 连续
	// 分配，失败不消耗 ID——为避免依赖成败，ID 一律按"尝试次序"给出，
	// 越界/失败的编号在两侧同样只会得到"评审不存在"）。
	var reviewIDs []int
	createAt := map[int]bool{}
	nextRID := 1
	for step := 0; step < steps; step++ {
		switch rng.Intn(10) {
		case 0, 1: // 建评审（约 20% 比例）
			createAt[step] = true
			reviewIDs = append(reviewIDs, nextRID)
			nextRID++
		}
	}

	for step := 0; step < steps; step++ {
		app := 101 + rng.Intn(nApp)
		if createAt[step] {
			n := []int{1, 3, 5}[rng.Intn(3)]
			reqB := rng.Intn(2)
			ops = append(ops, Op{"create", []int{app, n, reqB}})
			continue
		}
		// 尚未尝试建评审的前缀步只允许登记类操作。
		if len(reviewIDs) == 0 {
			if rng.Intn(2) == 0 {
				ops = append(ops, Op{pick(rng, "relation", "recusalRequest"),
					[]int{app, 1 + rng.Intn(nRev)}})
			}
			continue
		}
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // 投票（评委编号含越界，覆盖无权限）
			rid := reviewIDs[rng.Intn(len(reviewIDs))]
			judge := 1 + rng.Intn(nRev+2) // 可能越界 nRev+1/nRev+2
			rnd := 1 + rng.Intn(2)
			ch := 1 + rng.Intn(3)
			ops = append(ops, Op{"vote", []int{rid, judge, rnd, ch}})
		case 4, 5: // 中途回避
			rid := reviewIDs[rng.Intn(len(reviewIDs))]
			judge := 1 + rng.Intn(nRev)
			ops = append(ops, Op{"recuse", []int{rid, judge}})
		case 8: // 异议
			rid := reviewIDs[rng.Intn(len(reviewIDs))]
			ops = append(ops, Op{"objection", []int{rid}})
		case 9:
			rid := reviewIDs[rng.Intn(len(reviewIDs))]
			if rng.Intn(2) == 0 {
				ops = append(ops, Op{"adjudge", []int{rid, rng.Intn(2)}})
			} else {
				ops = append(ops, Op{"finalize", []int{rid}})
			}
		case 6, 7: // 新登记回避事实
			ops = append(ops, Op{pick(rng, "relation", "recusalRequest"),
				[]int{app, 1 + rng.Intn(nRev)}})
		}
	}
	return ops
}

func pick(rng *rand.Rand, a, b string) string {
	if rng.Intn(2) == 0 {
		return a
	}
	return b
}

// executeScenario 同时驱动两侧，逐步比对 (摘要, 错误类别) 与最终全状态。
func executeScenario(t *testing.T, seed int64, ops []Op) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	_ = rng
	m := newNaiveModel()
	s := NewService(testEpoch)
	prodClock.t = 0

	for i, op := range ops {
		mRes, mErr := m.apply(op)
		pRes, pErr := runProd(s, op)
		dlog("step %d op=%s => 朴素(%q,%s) 实现(%q,%s)", i, op, mRes, dash(mErr), pRes, dash(pErr))
		if mErr != pErr || mRes != pRes {
			for j := 0; j <= i; j++ {
				t.Logf("  seq[%d] = %s", j, ops[j])
			}
			t.Fatalf("seed=%d step=%d op=%s 分歧: 朴素=(%q,%s) 实现=(%q,%s)",
				seed, i, op, mRes, dash(mErr), pRes, dash(pErr))
		}
	}

	ms, ps := naiveSummary(m), stateSummary(s)
	if ms != ps {
		t.Fatalf("seed=%d 最终状态不一致\n朴素:\n%s\n实现:\n%s\n", seed, ms, ps)
	}
}

func dash(s string) string {
	if s == "" {
		return "成功"
	}
	return s
}

func TestDifferentialAgainstNaiveModel(t *testing.T) {
	runs := 300
	if testing.Short() {
		runs = 30
	}
	for seed := int64(1); seed <= int64(runs); seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genScenario(rng)
		dlog("==== seed=%d, %d ops ====", seed, len(ops))
		executeScenario(t, seed, ops)
	}
	t.Logf("差分测试 %d 条随机序列全部一致", runs)
}

var _ = fmt.Sprintf
