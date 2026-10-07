// Package difftest 用独立朴素参照对生产实现做大规模随机差分测试。
package difftest

import (
	"math/rand"
	"sort"
	"testing"

	ont "ontology/ontology"
	"ontology/ontology/naive"
)

// TestRandomDifferentialVsNaive 在大量随机关系图与动作上，把生产实现的
// （裁决, 跳过记录, 终态, 时钟, 审计）与独立朴素参照逐项对照。
func TestRandomDifferentialVsNaive(t *testing.T) {
	const iterations = 3000
	rng := rand.New(rand.NewSource(20261007))

	for iter := 0; iter < iterations; iter++ {
		env := GenEnv(rng, iter)
		action := env.GenAction(rng, iter)

		prodStore := env.Store()
		prodEng := ont.NewEngine(prodStore, env.Decider(), nil)
		prodRep, prodErr := prodEng.Execute(action)

		refWorld := naive.NewWorld(env.Snapshot(), env.NaivePolicies())
		refOut := refWorld.Run(action)

		var prodKind ont.ErrorKind
		if prodErr != nil {
			prodKind = prodErr.Kind
		}
		if prodKind != refOut.Reason {
			t.Fatalf("iter=%d verdict mismatch: prod=%d ref=%d\naction=%+v\n%s",
				iter, prodKind, refOut.Reason, action, env.Dump())
		}
		if prodRep.Committed != refOut.Committed {
			t.Fatalf("iter=%d commit mismatch prod=%v ref=%v", iter, prodRep.Committed, refOut.Committed)
		}

		if refOut.Committed {
			if !SkippedEqual(prodRep.Skipped, refOut.Skipped) {
				t.Fatalf("iter=%d skipped mismatch:\nprod=%+v\nref =%+v\n%s",
					iter, prodRep.Skipped, refOut.Skipped, env.Dump())
			}
			if !WorldCmp(prodStore.Snapshot(), refOut.State) {
				t.Fatalf("iter=%d final state mismatch\nprod=%+v\nref=%+v\naction=%+v\n%s",
					iter, prodStore.Snapshot(), refOut.State, action, env.Dump())
			}
		} else {
			if !WorldCmp(prodStore.Snapshot(), env.Snapshot()) {
				t.Fatalf("iter=%d rejected prod mutated state", iter)
			}
			if !WorldCmp(refOut.State, env.Snapshot()) {
				t.Fatalf("iter=%d rejected reference mutated state", iter)
			}
		}
	}
}

// TestRandomActionSequencesVsNaive 对照多条动作串行执行后的累积终态。
func TestRandomActionSequencesVsNaive(t *testing.T) {
	const sequences, seqLen = 300, 8
	rng := rand.New(rand.NewSource(424242))
	for s := 0; s < sequences; s++ {
		env := GenEnv(rng, 10000+s)
		actions := make([]ont.ActionDeclaration, seqLen)
		for i := range actions {
			actions[i] = env.GenAction(rng, 20000+s*seqLen+i)
		}

		prodStore := env.Store()
		prodEng := ont.NewEngine(prodStore, env.Decider(), nil)
		refWorld := naive.NewWorld(env.Snapshot(), env.NaivePolicies())

		for i, a := range actions {
			rep, err := prodEng.Execute(a)
			out := refWorld.Run(a)
			var prodKind ont.ErrorKind
			if err != nil {
				prodKind = err.Kind
			}
			if prodKind != out.Reason {
				t.Fatalf("seq=%d step=%d verdict mismatch prod=%d ref=%d\nstep action=%+v\nprior=%+v\n%s",
					s, i, prodKind, out.Reason, a, actions[:i], env.Dump())
			}
			if rep.Committed != out.Committed {
				t.Fatalf("seq=%d step=%d commit mismatch", s, i)
			}
		}
		if !WorldCmp(prodStore.Snapshot(), refWorld.State()) {
			t.Fatalf("seq=%d accumulated final states differ", s)
		}
	}
}

// SkippedEqual 比较两份跳过记录（实例、深度、剪除规模、被抑制操作）。
func SkippedEqual(a, b []ont.SkippedInstance) bool {
	if len(a) != len(b) {
		return false
	}
	ca := append([]ont.SkippedInstance(nil), a...)
	cb := append([]ont.SkippedInstance(nil), b...)
	sort.Slice(ca, func(i, j int) bool { return ca[i].Instance < ca[j].Instance })
	sort.Slice(cb, func(i, j int) bool { return cb[i].Instance < cb[j].Instance })
	for i := range ca {
		x, y := ca[i], cb[i]
		if x.Instance != y.Instance || x.Depth != y.Depth || x.PrunedSubtree != y.PrunedSubtree {
			return false
		}
		ox := append([]ont.Operation(nil), x.WithheldEffects...)
		oy := append([]ont.Operation(nil), y.WithheldEffects...)
		sort.Slice(ox, func(i, j int) bool { return ox[i] < ox[j] })
		sort.Slice(oy, func(i, j int) bool { return oy[i] < oy[j] })
		if len(ox) != len(oy) {
			return false
		}
		for k := range ox {
			if ox[k] != oy[k] {
				return false
			}
		}
	}
	return true
}

// WorldCmp 比较两份完整可观察状态：实例（含版本/属性）、链接、时钟、审计。
func WorldCmp(a, b ont.WorldState) bool {
	if a.Clock != b.Clock || len(a.Instances) != len(b.Instances) {
		return false
	}
	for id, in := range a.Instances {
		other, ok := b.Instances[id]
		if !ok || !InstanceEqual(in, other) {
			return false
		}
	}
	ea, eb := edgeSet(a.Edges), edgeSet(b.Edges)
	if len(ea) != len(eb) {
		return false
	}
	for k, v := range ea {
		if eb[k] != v {
			return false
		}
	}
	if len(a.Audit) != len(b.Audit) {
		return false
	}
	for i := range a.Audit {
		x, y := a.Audit[i], b.Audit[i]
		if x.Seq != y.Seq || x.Action != y.Action || x.Subject != y.Subject {
			return false
		}
		if !SkippedEqual(x.Skipped, y.Skipped) {
			return false
		}
		ax := append([]ont.CascadeEffect(nil), x.Applied...)
		ay := append([]ont.CascadeEffect(nil), y.Applied...)
		SortEffect(ax)
		SortEffect(ay)
		if len(ax) != len(ay) {
			return false
		}
		for k := range ax {
			if ax[k].Target != ay[k].Target || ax[k].Op != ay[k].Op || ax[k].Depth != ay[k].Depth {
				return false
			}
		}
	}
	return true
}

func edgeSet(edges []ont.Edge) map[ont.Edge]int {
	m := map[ont.Edge]int{}
	for _, e := range edges {
		m[e]++
	}
	return m
}

// InstanceEqual 比较两个实例（含属性 map）。
func InstanceEqual(x, y ont.Instance) bool {
	if x.ID != y.ID || x.Type != y.Type || x.Version != y.Version || len(x.Attrs) != len(y.Attrs) {
		return false
	}
	for k, v := range x.Attrs {
		if y.Attrs[k] != v {
			return false
		}
	}
	return true
}

// SortEffect 对级联效果做确定性排序。
func SortEffect(e []ont.CascadeEffect) {
	sort.Slice(e, func(i, j int) bool {
		if e[i].Target != e[j].Target {
			return e[i].Target < e[j].Target
		}
		if e[i].Op != e[j].Op {
			return e[i].Op < e[j].Op
		}
		return e[i].Depth < e[j].Depth
	})
}
