package flowtree

import (
	"math/rand"
	"testing"
)

// TestAllocateConservationPseudoRandom 在多棵伪随机树上反复验证：
// 份额之和恒等于额度，且只有就绪流能拿到非零份额。
func TestAllocateConservationPseudoRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))

	for iter := 0; iter < 200; iter++ {
		a := New()
		n := 1 + rng.Intn(30)
		for id := 1; id <= n; id++ {
			parent := 0
			if id > 1 {
				parent = rng.Intn(id)
			}
			w := 1 + rng.Intn(256)
			if err := a.Open(id, parent, w, false); err != nil {
				t.Fatalf("iter %d Open %d: %v", iter, id, err)
			}
		}

		readySet := map[int]bool{}
		var ready []int
		for id := 1; id <= n; id++ {
			if rng.Intn(2) == 0 {
				ready = append(ready, id)
				readySet[id] = true
			}
		}

		quota := rng.Intn(5000)
		shares, err := a.Allocate(quota, ready)
		if err != nil {
			t.Fatalf("iter %d Allocate: %v", iter, err)
		}

		sum := 0
		for id, v := range shares {
			sum += v
			if v < 0 {
				t.Fatalf("iter %d 流 %d 出现负份额 %d", iter, id, v)
			}
			if !readySet[id] {
				t.Fatalf("iter %d 非就绪流 %d 拿到份额 %d", iter, id, v)
			}
		}
		if len(ready) > 0 && sum != quota {
			t.Fatalf("iter %d 份额之和=%d，期望额度 %d", iter, sum, quota)
		}
		if len(ready) == 0 && sum != 0 {
			t.Fatalf("iter %d 无就绪流但份额和=%d", iter, sum)
		}

		if iter == 0 {
			t.Logf("输入(示例): n=%d 随机权重树, T=%d, ready=%v | 输出: %v | 判定: 总和=%d=T",
				n, quota, ready, sortedShares(shares), sum)
		}
	}
	t.Log("输入: 200 棵种子固定的伪随机树（种子 20260930）| " +
		"判定: 每次份额和=额度、非就绪流恒 0、可确定性重放 -> 通过")
}
