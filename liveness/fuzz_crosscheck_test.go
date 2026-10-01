package liveness

import (
	"math/rand"
	"reflect"
	"testing"
)

// TestRandomizedAgainstNaive 用固定种子生成大量随机 CFG
// （含环、多后继、先用后定义等），逐块对照朴素逐轮迭代，
// 保证求解器恒等于最小不动点且可复现。
func TestRandomizedAgainstNaive(t *testing.T) {
	const iterations = 300
	rng := rand.New(rand.NewSource(20261001))
	vars := []string{"a", "b", "c", "d", "e"}

	for iter := 0; iter < iterations; iter++ {
		n := 1 + rng.Intn(8)
		spec := map[int]blockSpec{}
		for id := 0; id < n; id++ {
			var insts []Instruction
			for k := rng.Intn(4); k >= 0; k-- {
				var uses, defs []string
				for _, v := range vars {
					switch rng.Intn(3) {
					case 0:
						uses = append(uses, v)
					case 1:
						defs = append(defs, v)
					case 2:
						uses = append(uses, v)
						defs = append(defs, v) // 同指令先用后定义
					}
				}
				insts = append(insts, inst(uses, defs))
			}
			var succ []int
			for s := 0; s < n; s++ {
				if rng.Intn(3) == 0 {
					succ = append(succ, s)
				}
			}
			spec[id] = blockSpec{insts: insts, succ: succ}
		}

		// 打乱录入顺序，验证前向引用与录入顺序无关性。
		order := rng.Perm(n)
		a := buildAnalyzer(t, order, spec)
		if err := a.Seal(); err != nil {
			t.Fatalf("iter %d: 后继全部合法，封口不应失败: %v", iter, err)
		}
		snaps, err := a.Blocks()
		if err != nil {
			t.Fatal(err)
		}
		ref := naiveReference(order, spec)
		for _, s := range snaps {
			rb := ref[s.ID]
			if !reflect.DeepEqual(s.UE, boolKeys(rb.ue)) ||
				!reflect.DeepEqual(s.Def, boolKeys(rb.def)) ||
				!reflect.DeepEqual(s.LiveIn, boolKeys(rb.in)) ||
				!reflect.DeepEqual(s.LiveOut, boolKeys(rb.out)) {
				t.Fatalf("iter %d B%d 与朴素迭代不一致:\nanalyzer=%+v\nnaive UE=%v Def=%v In=%v Out=%v",
					iter, s.ID, s, boolKeys(rb.ue), boolKeys(rb.def), boolKeys(rb.in), boolKeys(rb.out))
			}
		}
	}
	t.Logf("判定：%d 个固定种子随机 CFG（含环/多后继/同指令先用后定义）全部与朴素逐轮迭代逐项一致", iterations)
}
