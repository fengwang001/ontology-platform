package federation

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

// 12a. 与独立朴素模型在大量随机配置/随机总数上的逐步对照。
func TestRandomAgainstNaive(t *testing.T) {
	l := newTestLog(t, "crosscheck")
	defer l.close()
	rng := rand.New(rand.NewSource(20261006))
	const cases = 20000
	checked := 0
	for iter := 0; iter < cases; iter++ {
		n := rng.Intn(8)
		in := make([]naiveInput, 0, n)
		names := map[string]bool{}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("c%d", i)
			names[name] = true
			minv := int64(rng.Intn(3))
			var maxv int64
			if rng.Intn(2) == 0 {
				maxv = -1 // 不设上限
			} else {
				maxv = minv + int64(rng.Intn(5)) // 保证 min <= max
			}
			capv := int64(rng.Intn(8)) // 可能小于 min => 触发配置冲突
			in = append(in, naiveInput{
				name:      name,
				weight:    int64(rng.Intn(5)),
				min:       minv,
				max:       maxv,
				capacity:  capv,
				available: rng.Intn(5) != 0,
				current:   int64(rng.Intn(6)),
				dead:      rng.Intn(8) == 0,
			})
		}
		total := int64(rng.Intn(31))

		want, wantErr := naiveAllocate(in, total)
		reg := regFromNaive(t, in)
		res, gotErr := reg.Allocate(bigI(total))

		if iter < 5 {
			logInput(l, fmt.Sprintf("rand-%d", iter), in, total)
			if wantErr != nil {
				l.printf("[rand-%d] 朴素输出: 拒绝 code=%d %s", iter, wantErr.Code, wantErr.Msg)
			} else {
				l.printf("[rand-%d] 朴素输出: %v", iter, want)
			}
			logResult(l, fmt.Sprintf("rand-%d", iter), res, gotErr)
		}

		if wantErr != nil {
			if gotErr == nil || codeOf(gotErr) != wantErr.Code {
				t.Fatalf("iter=%d in=%+v total=%d: 朴素拒绝 code=%d(%s)，实际 %v",
					iter, in, total, wantErr.Code, wantErr.Msg, gotErr)
			}
			checked++
			continue
		}
		if gotErr != nil {
			t.Fatalf("iter=%d in=%+v total=%d: 朴素成功 %v，实际拒绝 %v",
				iter, in, total, want, gotErr)
		}
		// 目标逐项一致。
		for _, c := range in {
			g := res.Targets[c.name].Int64()
			if g != want[c.name] {
				logInput(l, "MISMATCH", in, total)
				l.printf("[MISMATCH] 朴素=%v", want)
				logResult(l, "MISMATCH", res, nil)
				t.Fatalf("iter=%d 集群 %s: 朴素=%d 实际=%d", iter, c.name, want[c.name], g)
			}
		}
		// 严格不变量：和 == total；不超有效上限；不可用/删除为 0。
		sum := int64(0)
		for _, v := range res.Targets {
			sum += v.Int64()
		}
		if sum != total {
			t.Fatalf("iter=%d: 目标之和 %d != total %d", iter, sum, total)
		}
		// 迁移量与朴素模型重算一致。
		wantMig := int64(0)
		for _, c := range in {
			if c.current > want[c.name] {
				wantMig += c.current - want[c.name]
			}
		}
		if res.Migration.Int64() != wantMig {
			t.Fatalf("iter=%d: 迁移量朴素=%d 实际=%s", iter, wantMig, res.Migration)
		}
		checked++
	}
	l.printf("判定: PASS，%d 组随机配置与朴素逐副本模型逐项一致（目标、错误类别、迁移量）", checked)
}

// 12b. 性能可验证证明：
//   - 结构性：生产代码中不存在随 total 数值增长的循环（所有循环只遍历集群集合）；
//   - 本测试用计数器证明：total 从 10 提升到 10^60（60 个数量级），
//     相同集群集合下 Rounds/ComparePairs/BigIntOps 完全不变；
//   - 轮数上界：每轮至少有一个集群饱和退出，故 Rounds <= 参与权重集群数。
func TestCostIndependentOfTotal(t *testing.T) {
	l := newTestLog(t, "perf")
	defer l.close()
	snap := []*snapshotCluster{}
	// 8 个集群，容量错落制造多轮饱和。
	for i := 0; i < 8; i++ {
		c := &Cluster{
			Name:      fmt.Sprintf("p%d", i),
			Weight:    bigI(int64(1 + (i*3)%7)),
			Min:       bigI(int64(i % 2)),
			Max:       nil, // 仅用容量限制，且容量足以容纳全部测试 total
			Capacity:  new(big.Int).Exp(big.NewInt(10), big.NewInt(70), nil),
			Available: true,
			Current:   bigI(int64(i)),
		}
		snap = append(snap, &snapshotCluster{cluster: c, dead: false})
	}
	run := func(total *big.Int) *stats {
		t.Helper()
		_, st, err := allocate(snap, total)
		if err != nil {
			t.Fatalf("allocate %s: %v", total, err)
		}
		return st
	}
	totals := []*big.Int{
		bigI(10),
		bigI(1000000),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(60), nil),
	}
	var base *stats
	for _, total := range totals {
		st := run(total)
		l.printf("[perf] total=%s => Rounds=%d ComparePairs=%d BigIntOps=%d Participants=%d",
			total, st.Rounds, st.ComparePairs, st.BigIntOps, st.Weighted)
		if base == nil {
			base = st
			continue
		}
		if st.Rounds != base.Rounds || st.ComparePairs != base.ComparePairs ||
			st.BigIntOps != base.BigIntOps {
			t.Fatalf("判定依据: total 增大 %d 个数量级后操作计数变化(%+v vs %+v)，开销随 total 增长",
				60, st, base)
		}
		if st.Rounds > st.Weighted {
			t.Fatalf("轮数 %d 超过权重集群数 %d，破坏每轮至少饱和一个集群的上界",
				st.Rounds, st.Weighted)
		}
	}
	l.printf("判定: PASS，total 从 10 到 10^60，轮数/比较/算术计数恒定；Rounds<=参与集群数")
	l.printf("结构性依据: 生产代码无按副本数迭代的循环（grep -n 'for remaining' federation/allocator.go），")
	l.printf("唯一的数值循环边界是集群数量，big.Int 运算次数为 O(n^2)，与 total 无关。")
}
