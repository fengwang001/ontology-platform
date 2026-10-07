package compensation

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// naiveModel 是独立实现的朴素顺序补偿模型：
// 单线程、无锁、无守卫，直接在 map 上顺序生效，
// 失败时从栈顶逐一弹出逆操作并执行；逆操作失败/异常同样汇总并冻结失败对象。
// 它与生产实现共享 Graph 类型之外的全部状态表达，用于对拍最终可观察状态。
type naiveModel struct {
	props map[string]map[string]any
	ver   map[string]uint64
	clock map[string]uint64
	links map[string]*naiveLink
	taint map[string]int
}

type naiveLink struct {
	from, to, typ string
	alive         bool
}

type naiveInverse func() error

type naiveStep struct {
	index   int
	objects []string
	fail    bool
	panics  bool
	apply   naiveInverse
}

func newNaiveModel(seed *Graph) *naiveModel {
	m := &naiveModel{
		props: map[string]map[string]any{},
		ver:   map[string]uint64{},
		clock: map[string]uint64{},
		links: map[string]*naiveLink{},
		taint: map[string]int{},
	}
	s := takeSnapshot(seed)
	for id, p := range s.props {
		cp := make(map[string]any, len(p))
		for k, v := range p {
			cp[k] = v
		}
		m.props[id] = cp
		m.ver[id] = s.ver[id]
		m.clock[id] = s.clock[id]
	}
	for id, alive := range s.links {
		// 快照只有存活态，from/to 由调用方在 seedGraph 后单独补齐意义不大；
		// 对拍序列只使用模型内部新建的链接。
		if alive {
			m.links[id] = &naiveLink{alive: true}
		}
	}
	return m
}

// runAction 在朴素模型上执行一个动作序列定义，返回类别与污染的对象集合。
func (m *naiveModel) runAction(ops []SubOp) Category {
	for _, op := range ops {
		for _, id := range opObjectIDs(op) {
			if _, bad := m.taint[id]; bad {
				return CategoryContaminated
			}
		}
	}
	var stack []naiveStep
	failed := -1
	for i, op := range ops {
		if op.Check != nil && !op.Check() {
			failed = i
			break
		}
		st, err := m.naiveEffect(op, i)
		if err != nil {
			failed = i
			break
		}
		stack = append(stack, st)
	}
	if failed == -1 {
		return CategoryOK
	}

	poisoned := map[string]struct{}{}
	earliestFor := map[string]int{}
	hardFailure := false
	for i := len(stack) - 1; i >= 0; i-- {
		st := stack[i]
		touch := false
		for _, id := range st.objects {
			if _, ok := poisoned[id]; ok {
				touch = true
			}
		}
		if touch {
			hardFailure = true
			// 跳过只冻结状态，不产生新的污染对象。
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					hardFailure = true
					for _, id := range st.objects {
						poisoned[id] = struct{}{}
						if prev, ok := earliestFor[id]; !ok || st.index < prev {
							earliestFor[id] = st.index
						}
					}
				}
			}()
			if st.panics {
				panic("injected")
			}
			if st.fail {
				hardFailure = true
				for _, id := range st.objects {
					poisoned[id] = struct{}{}
					if prev, ok := earliestFor[id]; !ok || st.index < prev {
						earliestFor[id] = st.index
					}
				}
				return
			}
			if err := st.apply(); err != nil {
				hardFailure = true
				for _, id := range st.objects {
					poisoned[id] = struct{}{}
					if prev, ok := earliestFor[id]; !ok || st.index < prev {
						earliestFor[id] = st.index
					}
				}
			}
		}()
	}

	for id := range poisoned {
		if _, exists := m.taint[id]; !exists {
			m.taint[id] = earliestFor[id]
		}
	}
	if hardFailure {
		return CategoryCompensationFailed
	}
	return CategoryBusinessRejected
}

func (m *naiveModel) naiveEffect(op SubOp, i int) (naiveStep, error) {
	switch op.Kind {
	case OpSetProperties:
		p, ok := m.props[op.ObjectID]
		if !ok {
			return naiveStep{}, fmt.Errorf("no object")
		}
		old := map[string]any{}
		had := map[string]bool{}
		for k, v := range op.Sets {
			cur, exists := p[k]
			old[k], had[k] = cur, exists
			_ = v
		}
		for k, v := range op.Sets {
			p[k] = v
		}
		m.clock[op.ObjectID]++
		m.ver[op.ObjectID]++
		id := op.ObjectID
		return naiveStep{
			index: i, objects: []string{id},
			fail: op.InjectInverseFailure, panics: op.InjectInversePanic,
			apply: func() error {
				for k := range op.Sets {
					if had[k] {
						m.props[id][k] = old[k]
					} else {
						delete(m.props[id], k)
					}
				}
				m.clock[id]--
				m.ver[id]--
				return nil
			},
		}, nil
	case OpHook:
		if _, ok := m.props[op.ObjectID]; !ok {
			return naiveStep{}, fmt.Errorf("no object")
		}
		id := op.ObjectID
		return naiveStep{index: i, objects: []string{id},
			fail: op.InjectInverseFailure, panics: op.InjectInversePanic,
			apply: func() error { return nil }}, nil
	case OpCreateLink:
		if l, ok := m.links[op.LinkID]; ok && l.alive {
			return naiveStep{}, fmt.Errorf("exists")
		}
		m.links[op.LinkID] = &naiveLink{from: op.From, to: op.To, typ: op.LinkType, alive: true}
		for _, id := range nonEmpty(op.From, op.To) {
			m.clock[id]++
			m.ver[id]++
		}
		return naiveStep{index: i, objects: nonEmpty(op.From, op.To),
			fail: op.InjectInverseFailure, panics: op.InjectInversePanic,
			apply: func() error {
				l, ok := m.links[op.LinkID]
				if !ok || !l.alive {
					return fmt.Errorf("missing")
				}
				l.alive = false
				for _, id := range nonEmpty(op.From, op.To) {
					m.clock[id]--
					m.ver[id]--
				}
				return nil
			}}, nil
	case OpDeleteLink:
		l, ok := m.links[op.LinkID]
		if !ok || !l.alive {
			return naiveStep{}, fmt.Errorf("missing")
		}
		l.alive = false
		for _, id := range nonEmpty(l.from, l.to) {
			m.clock[id]++
		}
		from, to := l.from, l.to
		return naiveStep{index: i, objects: nonEmpty(from, to),
			fail: op.InjectInverseFailure, panics: op.InjectInversePanic,
			apply: func() error {
				ll, ok2 := m.links[op.LinkID]
				if !ok2 {
					return fmt.Errorf("missing")
				}
				if ll.alive {
					return fmt.Errorf("alive")
				}
				ll.alive = true
				for _, id := range nonEmpty(from, to) {
					m.clock[id]--
				}
				return nil
			}}, nil
	default:
		return naiveStep{}, fmt.Errorf("unknown")
	}
}

// randomOps 生成随机动作序列：只触及 seed 中存在的对象与干净的链接名，
// 在随机位置注入业务失败、逆操作失败与逆操作异常。
func randomOps(rng *rand.Rand, nObjects int) []SubOp {
	n := 1 + rng.Intn(6)
	ops := make([]SubOp, 0, n)
	failAt := rng.Intn(n + 2) // 可能不失败（>=n）
	for i := 0; i < n; i++ {
		obj := fmt.Sprintf("o%d", rng.Intn(nObjects))
		var op SubOp
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4, 5, 6, 7:
			op = SubOp{
				Kind:     OpSetProperties,
				ObjectID: obj,
				Sets:     map[string]any{"k" + fmt.Sprint(rng.Intn(3)): rng.Intn(1000)},
			}
		case 8:
			op = SubOp{Kind: OpHook, ObjectID: obj, Hook: "v"}
		case 9:
			op = SubOp{
				Kind:   OpCreateLink,
				LinkID: fmt.Sprintf("randL%d-%d", n, i),
				From:   obj,
				To:     fmt.Sprintf("o%d", rng.Intn(nObjects)), LinkType: "rel",
			}
		}
		if rng.Intn(4) == 0 {
			op.InjectInverseFailure = true
		}
		if rng.Intn(20) == 0 {
			op.InjectInversePanic = true
			op.InjectInverseFailure = false
		}
		if i == failAt {
			step := i
			op.Check = func() bool { _ = step; return false }
		}
		ops = append(ops, op)
	}
	return ops
}

// TestDifferentialAgainstNaiveModel 与朴素顺序补偿模型对拍：
// 同一份随机输入与故障注入点，生产实现与朴素模型的最终可观察状态必须逐字段一致。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	const iterations = 400
	rng := rand.New(rand.NewSource(20261007))

	for it := 0; it < iterations; it++ {
		const nObjects = 5

		g := NewGraph()
		for i := 0; i < nObjects; i++ {
			g.AddObject(fmt.Sprintf("o%d", i),
				map[string]any{"k0": 0, "k1": 0, "k2": 0})
		}
		model := newNaiveModel(g)

		exec := New(g, nil)

		seqLen := 1 + rng.Intn(4)
		var allOps [][]SubOp
		for s := 0; s < seqLen; s++ {
			allOps = append(allOps, randomOps(rng, nObjects))
		}

		for s, ops := range allOps {
			// 污染拒绝：朴素模型一旦已污染某对象，生产实现必须报 CONTAMINATED。
			touchesTaint := false
			for _, op := range ops {
				for _, id := range opObjectIDs(op) {
					if _, bad := model.taint[id]; bad {
						touchesTaint = true
					}
				}
			}
			out := exec.Execute(context.Background(),
				&Action{ID: fmt.Sprintf("it%d-seq%d", it, s), Ops: ops})
			if touchesTaint {
				if out.Category != CategoryContaminated {
					t.Fatalf("it %d seq %d: want CONTAMINATED, got %s", it, s, out.Category)
				}
				continue
			}
			wantCat := model.runAction(ops)
			if out.Category != wantCat {
				t.Fatalf("it %d seq %d: real=%s naive=%s", it, s, out.Category, wantCat)
			}
		}

		// 最终状态逐字段对拍。
		got := takeSnapshot(g)
		for id, wantProps := range model.props {
			gotProps := got.props[id]
			for k, v := range wantProps {
				if gotProps[k] != v {
					t.Fatalf("it %d: %s.%s real=%v naive=%v; ops=%+v; taints=%v",
						it, id, k, gotProps[k], v, describeOps(allOps), model.taint)
				}
			}
			for k := range gotProps {
				if _, ok := wantProps[k]; !ok {
					t.Fatalf("it %d: %s.%s leaked in real graph", it, id, k)
				}
			}
			if got.ver[id] != model.ver[id] {
				t.Fatalf("it %d: %s version real=%d naive=%d", it, id, got.ver[id], model.ver[id])
			}
			if got.clock[id] != model.clock[id] {
				t.Fatalf("it %d: %s clock real=%d naive=%d", it, id, got.clock[id], model.clock[id])
			}
		}
		for id, l := range model.links {
			if got.links[id] != l.alive {
				t.Fatalf("it %d: link %s real=%v naive=%v", it, id, got.links[id], l.alive)
			}
		}
		for id := range model.taint {
			if _, ok := got.ver[id]; !ok {
				continue
			}
			info, tainted := g.Tainted(id)
			if !tainted {
				t.Fatalf("it %d: %s tainted in naive model but not in real graph", it, id)
			}
			if info.EarliestStep != model.taint[id] {
				t.Fatalf("it %d: %s earliest taint real=%d naive=%d",
					it, id, info.EarliestStep, model.taint[id])
			}
		}
	}
}
