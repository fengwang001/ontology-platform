package compensate

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// genSpec 随机生成一个分支依赖 DAG（保证无环）与故障注入点。
// 为满足“无依赖分支写入集不相交”，每条分支使用独占对象命名空间 br<i>。
// 有依赖关系的分支可复用对象槽位（由允许重叠规则保证安全），这里仍用独立命名空间以简化。
func genSpec(rng *rand.Rand, id string) ActionSpec {
	n := 1 + rng.Intn(6)
	branches := make([]BranchSpec, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("b%d", i)
		deps := []string{}
		// 只可能依赖更早的分支 => 无环。
		possible := append([]string{}, earlierNames(branches)...)
		rng.Shuffle(len(possible), func(a, b int) { possible[a], possible[b] = possible[b], possible[a] })
		for _, d := range possible {
			if rng.Intn(3) == 0 {
				deps = append(deps, d)
			}
		}
		opsN := 1 + rng.Intn(3)
		ops := make([]WriteOp, 0, opsN)
		for j := 0; j < opsN; j++ {
			// 每条分支独占对象命名空间：即便有依赖关系也不复用其它分支的槽位，
			// 从而并发引擎与朴素串行模型对每个槽位捕获的旧值/最终值必然逐槽一致。
			// （依赖分支复用槽位的语义由专门单测 TestDependencyChainCompensationOrder 覆盖。）
			op := WriteOp{
				ID:       fmt.Sprintf("%s-op%d", name, j),
				ObjectID: fmt.Sprintf("obj-%s", name),
				Property: fmt.Sprintf("p%d", j),
				Value:    fmt.Sprintf("%s-v%d", name, j),
			}
			// 约 20% 概率在正操作注入失败，且失败点之后不再生成后续操作（它们不会生效）。
			if rng.Intn(5) == 0 {
				op.FailApply = true
				ops = append(ops, op)
				break
			}
			// 已生效子操作约 15% 概率逆操作失败。
			if rng.Intn(7) == 0 {
				op.FailCompensate = true
			}
			ops = append(ops, op)
		}
		branches = append(branches, BranchSpec{Name: name, Deps: deps, Ops: ops})
	}
	return ActionSpec{ID: id, Branches: branches}
}

func earlierNames(bs []BranchSpec) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

func kindHistogram(recs []ErrorRecord) map[ErrKind]int {
	h := map[ErrKind]int{}
	for _, r := range recs {
		h[r.Kind]++
	}
	return h
}

func inverseKeySet(recs []ErrorRecord) map[string]bool {
	s := map[string]bool{}
	for _, r := range recs {
		if r.Kind == KindInverseFailed {
			s[fmt.Sprintf("%s#%d", r.Branch, r.OpIndex)] = true
		}
	}
	return s
}

// 对拍：随机 DAG + 故障注入，并发引擎与朴素串行模型在独立图上运行，
// 最终对象图 / 已生效集 / 已补偿集 / 阻塞集 / 各类错误计数 / 逆操作失败位置必须一致。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	const iterations = 400
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		spec := genSpec(rng, fmt.Sprintf("ACT%d", seed))

		g1 := NewGraph()
		e1 := NewEngine(g1, nil)
		a1, err := e1.Declare(spec)
		if err != nil {
			t.Fatalf("seed %d: declare: %v\nspec=%+v", seed, err, spec)
		}
		rep := a1.Execute(context.Background())

		g2 := NewGraph()
		nr, err := RunNaive(g2, spec, nil)
		if err != nil {
			t.Fatalf("seed %d: naive declare: %v", seed, err)
		}

		if !reflect.DeepEqual(g1.Snapshot(), nr.Final) {
			t.Fatalf("seed %d: final graph mismatch\nengine=%v\nnaive =%v\nspec=%+v",
				seed, g1.Snapshot(), nr.Final, spec)
		}
		if !reflect.DeepEqual(rep.Applied, nr.Applied) {
			t.Fatalf("seed %d: applied mismatch engine=%v naive=%v", seed, rep.Applied, nr.Applied)
		}
		if !reflect.DeepEqual(rep.Compensated, nr.Compensated) {
			t.Fatalf("seed %d: compensated mismatch engine=%v naive=%v", seed, rep.Compensated, nr.Compensated)
		}
		if !reflect.DeepEqual(sortedCopy(rep.Blocked), sortedCopy(nr.Blocked)) {
			t.Fatalf("seed %d: blocked mismatch engine=%v naive=%v", seed, rep.Blocked, nr.Blocked)
		}
		if !reflect.DeepEqual(kindHistogram(rep.Records), kindHistogram(nr.Records)) {
			t.Fatalf("seed %d: error histogram mismatch engine=%v naive=%v",
				seed, kindHistogram(rep.Records), kindHistogram(nr.Records))
		}
		if !reflect.DeepEqual(inverseKeySet(rep.Records), inverseKeySet(nr.Records)) {
			t.Fatalf("seed %d: inverse failure sites mismatch engine=%v naive=%v",
				seed, inverseKeySet(rep.Records), inverseKeySet(nr.Records))
		}
	}
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
