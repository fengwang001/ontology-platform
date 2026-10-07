package difftest

import (
	"fmt"
	"math/rand"
	"strings"

	ont "ontology/ontology"
	"ontology/ontology/naive"
)

// Env 是一次差分用例的夹具：小规模随机图 + 每主体的可见性/授权矩阵。
// 它同时生成生产侧 Decider 与参照侧 naive.Policy，保证两侧观察到的是
// 同一份授权事实，但裁决算法各自独立实现。
type Env struct {
	ids      []ont.InstanceID
	insts    []ont.Instance
	edges    []ont.Edge
	dec      *matrixDecider
	policies map[ont.SubjectID]naive.Policy
}

// matrixDecider 是生产侧 Decider 的一个矩阵实现，仅用于测试对照。
type matrixDecider struct {
	invisible map[ont.InstanceID]bool
	allow     map[ont.InstanceID]map[ont.Operation]map[int]bool
}

func (d *matrixDecider) Visible(_ ont.SubjectID, in ont.Instance, _ int) bool {
	return !d.invisible[in.ID]
}

func (d *matrixDecider) Allowed(_ ont.SubjectID, in ont.Instance, op ont.Operation, depth int) (bool, bool) {
	if byOp, ok := d.allow[in.ID]; ok {
		if m, ok := byOp[op]; ok {
			if v, has := m[depth]; has {
				return v, false
			}
		}
	}
	return false, true
}

// GenEnv 生成一个随机图世界及其授权矩阵。
func GenEnv(rng *rand.Rand, seed int) *Env {
	n := 2 + rng.Intn(7) // 2..8 个实例，小世界高密度覆盖边界
	env := &Env{
		dec: &matrixDecider{
			invisible: map[ont.InstanceID]bool{},
			allow:     map[ont.InstanceID]map[ont.Operation]map[int]bool{},
		},
		policies: map[ont.SubjectID]naive.Policy{
			"s1": {
				Invisible: map[ont.InstanceID]bool{},
				Allow:     map[ont.InstanceID]map[ont.Operation]map[int]bool{},
			},
		},
	}
	types := []ont.ObjectTypeID{"doc", "folder", "person"}
	for i := 0; i < n; i++ {
		id := ont.InstanceID(fmt.Sprintf("i%d", i))
		env.ids = append(env.ids, id)
		env.insts = append(env.insts, ont.Instance{
			ID: id, Type: types[rng.Intn(len(types))], Version: 1,
			Attrs: map[string]string{"v": fmt.Sprintf("%d", rng.Intn(3))},
		})
	}
	linkTypes := []ont.LinkTypeID{"rel", "parent"}
	edgeSeen := map[ont.Edge]bool{}
	for i := 0; i < rng.Intn(n*2); i++ {
		a, b := env.ids[rng.Intn(n)], env.ids[rng.Intn(n)]
		if a == b && rng.Intn(2) == 0 {
			continue
		}
		e := ont.Edge{LinkType: linkTypes[rng.Intn(len(linkTypes))], From: a, To: b}
		if !edgeSeen[e] {
			edgeSeen[e] = true
			env.edges = append(env.edges, e)
		}
	}

	pol := env.policies["s1"]
	for _, id := range env.ids {
		if rng.Intn(100) < 15 {
			pol.Invisible[id] = true
			env.dec.invisible[id] = true
			continue
		}
		for _, op := range []ont.Operation{ont.OpUpdate, ont.OpDelete} {
			if rng.Intn(100) < 25 {
				continue // 该 op 全深度弃权
			}
			for lvl := 0; lvl <= 3; lvl++ {
				if rng.Intn(100) < 35 {
					continue // 该深度弃权
				}
				v := rng.Intn(100) < 70
				ensureLevel(pol.Allow, id, op)[lvl] = v
				ensureLevel(env.dec.allow, id, op)[lvl] = v
			}
		}
	}
	return env
}

func ensureLevel(m map[ont.InstanceID]map[ont.Operation]map[int]bool,
	id ont.InstanceID, op ont.Operation) map[int]bool {
	if m[id] == nil {
		m[id] = map[ont.Operation]map[int]bool{}
	}
	if m[id][op] == nil {
		m[id][op] = map[int]bool{}
	}
	return m[id][op]
}

// Store 返回一份全新的生产侧内存存储。
func (e *Env) Store() *ont.MemStore {
	st := ont.NewMemStore()
	for _, in := range e.insts {
		st.AddInstance(in)
	}
	for _, ed := range e.edges {
		st.AddEdge(ed)
	}
	return st
}

// Decider 返回生产侧授权判定。
func (e *Env) Decider() ont.Decider { return e.dec }

// Snapshot 返回初始世界快照。
func (e *Env) Snapshot() ont.WorldState { return e.Store().Snapshot() }

// NaivePolicies 返回参照侧策略表。
func (e *Env) NaivePolicies() map[ont.SubjectID]naive.Policy { return e.policies }

// Dump 输出可读的图与授权事实，供差分失败时定位。
func (e *Env) Dump() string {
	var b strings.Builder
	for _, in := range e.insts {
		fmt.Fprintf(&b, "  %s(%s) attrs=%v\n", in.ID, in.Type, in.Attrs)
	}
	for _, ed := range e.edges {
		fmt.Fprintf(&b, "  %s -%s-> %s\n", ed.From, ed.LinkType, ed.To)
	}
	for id := range e.policies["s1"].Invisible {
		fmt.Fprintf(&b, "  invisible %s\n", id)
	}
	for id, byOp := range e.policies["s1"].Allow {
		fmt.Fprintf(&b, "  allow %s %v\n", id, byOp)
	}
	return b.String()
}

// GenAction 在该世界上生成一个随机合法动作。
func (e *Env) GenAction(rng *rand.Rand, seed int) ont.ActionDeclaration {
	root := e.ids[rng.Intn(len(e.ids))]
	op := []ont.Operation{ont.OpUpdate, ont.OpDelete}[rng.Intn(2)]

	ruleSet := map[ont.LinkTypeID]bool{}
	var rules []ont.CascadeRule
	for i := 0; i < 1+rng.Intn(2); i++ {
		lt := []ont.LinkTypeID{"rel", "parent"}[rng.Intn(2)]
		if ruleSet[lt] {
			continue
		}
		ruleSet[lt] = true
		rules = append(rules, ont.CascadeRule{
			LinkType:    lt,
			Outgoing:    rng.Intn(100) < 70,
			Effect:      []ont.Operation{ont.OpUpdate, ont.OpDelete}[rng.Intn(2)],
			NoPropagate: rng.Intn(100) < 15,
		})
	}

	return ont.ActionDeclaration{
		Name:    fmt.Sprintf("a%d", seed),
		Subject: "s1",
		Direct: []ont.DirectOp{{
			Op: op, Type: e.typeOf(root), Target: root,
			NewAttrs: map[string]string{"a": "1"},
		}},
		Cascades:      rules,
		MaxDepth:      1 + rng.Intn(4),
		InvisibleMode: []ont.OnInvisible{ont.RejectOnInvisible, ont.SkipOnInvisible}[rng.Intn(2)],
		Merge:         []ont.MergePolicy{ont.MergeAll, ont.MergeAny}[rng.Intn(2)],
	}
}

func (e *Env) typeOf(id ont.InstanceID) ont.ObjectTypeID {
	for _, in := range e.insts {
		if in.ID == id {
			return in.Type
		}
	}
	return "doc"
}
