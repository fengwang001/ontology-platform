package capture

import (
	"fmt"
	"math/rand"
	"testing"
)

// genOps 用给定种子生成一串“有意义”的随机操作。
// 生成过程中在一个朴素模型上同步推进，从而知道真实栈顶与存活句柄：
// 槽参数围绕栈顶分布（含越界与负值），句柄参数从“存活句柄 + 可能已释放/不存在值”中抽取。
func genOps(rng *rand.Rand, n int) []op {
	ops := make([]op, 0, n)
	probe := newNaiveModel()
	var alive []int // 当前持有数 > 0 的句柄

	randomSlot := func() int {
		return rng.Intn(probe.top()+3) - 1 // 偶尔 -1 或 == 栈顶
	}
	pickHandle := func() int {
		if len(alive) > 0 && rng.Intn(5) != 0 {
			return alive[rng.Intn(len(alive))]
		}
		return []int{1, 2, 999, 100000}[rng.Intn(4)]
	}

	for len(ops) < n {
		var o op
		switch rng.Intn(10) {
		case 0, 1:
			o = op{kind: "push", a: rng.Intn(1000) - 500}
		case 2:
			o = op{kind: "capture", a: randomSlot()}
		case 3:
			o = op{kind: "slot_get", a: randomSlot()}
		case 4:
			o = op{kind: "slot_set", a: randomSlot(), b: rng.Intn(1000)}
		case 5:
			o = op{kind: "close", a: rng.Intn(probe.top()+3) - 1}
		case 6:
			o = op{kind: "handle_get", a: pickHandle()}
		case 7:
			o = op{kind: "handle_set", a: pickHandle(), b: rng.Intn(1000) - 500}
		case 8:
			o = op{kind: "release", a: pickHandle()}
		default:
			o = op{kind: "push", a: rng.Intn(100)} // 保证栈不会长期为空
		}

		r := applyNaive(probe, o)
		if o.kind == "capture" && r.ok && !containsInt(alive, r.val) {
			alive = append(alive, r.val)
		}
		if o.kind == "release" && r.ok {
			if v := probe.vars[o.a]; v != nil && v.holders == 0 {
				alive = removeInt(alive, o.a)
			}
		}
		ops = append(ops, o)
	}
	return ops
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func removeInt(xs []int, x int) []int {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

// TestRandomDifferential 多组随机序列逐步对照实现与朴素模拟。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		name := fmt.Sprintf("seed=%d", seed)
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			n := 120 + rng.Intn(120)
			runScenario(t, name, genOps(rng, n))
		})
	}
}

// FuzzDifferential 让 fuzzing 继续探索边界长度与参数组合。
func FuzzDifferential(f *testing.F) {
	f.Add(int64(1), 20)
	f.Fuzz(func(t *testing.T, seed int64, steps int) {
		if steps <= 0 || steps > 500 {
			return
		}
		rng := rand.New(rand.NewSource(seed))
		runScenario(t, fmt.Sprintf("fuzz-seed=%d-steps=%d", seed, steps), genOps(rng, steps))
	})
}
