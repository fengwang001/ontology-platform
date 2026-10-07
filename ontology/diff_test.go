package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// 随机化差异测试：在大量随机生成的关系图与授权声明序列上，
// 逐项对照 Engine（不动点物化）与 NaiveEngine（朴素路径枚举）的判定。
func TestDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDiffSequence(t, rand.New(rand.NewSource(seed)), 150)
		})
	}
}

type diffEnv struct {
	e           *Engine
	n           *NaiveEngine
	objectTypes []string
	linkTypes   []string
	tags        []string
	roles       []string
	subjects    []string
	instances   []string
	edges       []LinkEdge
	atts        []Attachment
	props       []Propagation
	blocks      []Block
	grants      []Grant
}

func pick(r *rand.Rand, xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[r.Intn(len(xs))]
}

func removeStr(xs []string, x string) []string {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func runDiffSequence(t *testing.T, r *rand.Rand, steps int) {
	env := &diffEnv{e: New(nil), n: NewNaive()}
	// 初始骨架，保证后续操作有合法目标可选。
	for i := 0; i < 4; i++ {
		ot := fmt.Sprintf("OT%d", i)
		env.e.AddObjectType(ot)
		env.n.AddObjectType(ot)
		env.objectTypes = append(env.objectTypes, ot)
	}
	for i := 0; i < 2; i++ {
		lt := fmt.Sprintf("LT%d", i)
		env.e.AddLinkType(lt)
		env.n.AddLinkType(lt)
		env.linkTypes = append(env.linkTypes, lt)
	}
	for i := 0; i < 3; i++ {
		tag := fmt.Sprintf("T%d", i)
		env.e.AddTag(tag)
		env.n.AddTag(tag)
		env.tags = append(env.tags, tag)
	}
	env.e.AddRole("R0")
	env.n.AddRole("R0")
	env.roles = append(env.roles, "R0")

	for step := 0; step < steps; step++ {
		env.randomOp(t, r, step)
		env.compareAll(t, step)
	}
}

func (env *diffEnv) compareAll(t *testing.T, step int) {
	t.Helper()
	for _, s := range append(env.subjects, "ghost-subject") {
		for _, inst := range append(env.instances, "ghost-instance") {
			de, dn := env.e.Decide(s, inst), env.n.Decide(s, inst)
			if de.Allowed != dn.Allowed || de.Reason != dn.Reason {
				t.Fatalf("step %d: Decide(%s,%s) 不一致: engine=(%v,%s) naive=(%v,%s)",
					step, s, inst, de.Allowed, de.Reason, dn.Allowed, dn.Reason)
			}
			for _, tag := range append(env.tags, "ghost-tag") {
				de, dn := env.e.DecideTag(s, inst, tag), env.n.DecideTag(s, inst, tag)
				if de.Allowed != dn.Allowed || de.Reason != dn.Reason {
					t.Fatalf("step %d: DecideTag(%s,%s,%s) 不一致: engine=(%v,%s) naive=(%v,%s)",
						step, s, inst, tag, de.Allowed, de.Reason, dn.Allowed, dn.Reason)
				}
			}
		}
	}
}

// randomOp 生成一步随机变更；Engine 接受时才同步到 NaiveEngine，
// 保证两侧声明集始终一致。
func (env *diffEnv) randomOp(t *testing.T, r *rand.Rand, step int) {
	t.Helper()
	switch r.Intn(14) {
	case 0:
		ot := fmt.Sprintf("OT%d", len(env.objectTypes))
		if env.e.AddObjectType(ot) == nil {
			env.n.AddObjectType(ot)
			env.objectTypes = append(env.objectTypes, ot)
		}
	case 1:
		if len(env.linkTypes) == 0 || len(env.objectTypes) == 0 {
			return
		}
		edge := LinkEdge{LinkType: pick(r, env.linkTypes), From: pick(r, env.objectTypes), To: pick(r, env.objectTypes)}
		if env.e.AddLinkEdge(edge) == nil {
			env.n.AddLinkEdge(edge)
			env.edges = append(env.edges, edge)
		}
	case 2:
		if len(env.edges) == 0 {
			return
		}
		edge := env.edges[r.Intn(len(env.edges))]
		if env.e.RemoveLinkEdge(edge) == nil {
			env.n.RemoveLinkEdge(edge)
			env.edges = removeEdge(env.edges, edge)
		}
	case 3:
		if len(env.objectTypes) == 0 || len(env.tags) == 0 {
			return
		}
		att := Attachment{ObjectType: pick(r, env.objectTypes), Tag: pick(r, env.tags)}
		if env.e.AttachTag(att) == nil {
			env.n.AttachTag(att)
			env.atts = append(env.atts, att)
		}
	case 4:
		if len(env.atts) == 0 {
			return
		}
		att := env.atts[r.Intn(len(env.atts))]
		if env.e.DetachTag(att) == nil {
			env.n.DetachTag(att)
			env.atts = removeAtt(env.atts, att)
		}
	case 5:
		if len(env.tags) == 0 || len(env.linkTypes) == 0 {
			return
		}
		p := Propagation{Tag: pick(r, env.tags), LinkType: pick(r, env.linkTypes), Direction: Direction(r.Intn(2))}
		if env.e.AddPropagation(p) == nil {
			env.n.AddPropagation(p)
			env.props = append(env.props, p)
		}
	case 6:
		if len(env.props) == 0 {
			return
		}
		p := env.props[r.Intn(len(env.props))]
		if env.e.RemovePropagation(p) == nil {
			env.n.RemovePropagation(p)
			env.props = removeProp(env.props, p)
		}
	case 7:
		if len(env.objectTypes) == 0 || len(env.tags) == 0 {
			return
		}
		b := Block{ObjectType: pick(r, env.objectTypes), Tag: pick(r, env.tags)}
		if env.e.AddBlock(b) == nil {
			env.n.AddBlock(b)
			env.blocks = append(env.blocks, b)
		}
	case 8:
		if len(env.blocks) == 0 {
			return
		}
		b := env.blocks[r.Intn(len(env.blocks))]
		if env.e.RemoveBlock(b) == nil {
			env.n.RemoveBlock(b)
			env.blocks = removeBlock(env.blocks, b)
		}
	case 9:
		role := fmt.Sprintf("R%d", len(env.roles))
		parents := []string{}
		for i := 0; i < r.Intn(3); i++ {
			parents = append(parents, pick(r, env.roles))
		}
		if r.Intn(10) == 0 { // 小概率制造自环/互环
			parents = append(parents, role)
		}
		if env.e.AddRole(role, parents...) == nil {
			env.n.AddRole(role, parents...)
			env.roles = append(env.roles, role)
		}
	case 10:
		if len(env.roles) == 0 || len(env.tags) == 0 {
			return
		}
		g := Grant{Role: pick(r, env.roles), Tag: pick(r, env.tags), Effect: Effect(r.Intn(2))}
		if env.e.AddGrant(g) == nil {
			env.n.AddGrant(g)
			env.grants = append(env.grants, g)
		}
	case 11:
		if len(env.grants) == 0 {
			return
		}
		g := env.grants[r.Intn(len(env.grants))]
		if env.e.RemoveGrant(g) == nil {
			env.n.RemoveGrant(g)
			env.grants = removeGrant(env.grants, g)
		}
	case 12:
		subj := fmt.Sprintf("S%d", len(env.subjects))
		roles := []string{}
		for i := 0; i < r.Intn(3); i++ {
			roles = append(roles, pick(r, env.roles))
		}
		if env.e.AddSubject(subj, roles...) == nil {
			env.n.AddSubject(subj, roles...)
			env.subjects = append(env.subjects, subj)
		}
	case 13:
		if len(env.objectTypes) == 0 {
			return
		}
		inst := fmt.Sprintf("I%d", len(env.instances))
		ot := pick(r, env.objectTypes)
		if env.e.AddInstance(inst, ot) == nil {
			env.n.AddInstance(inst, ot)
			env.instances = append(env.instances, inst)
		}
	}
}

func removeEdge(xs []LinkEdge, x LinkEdge) []LinkEdge {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func removeAtt(xs []Attachment, x Attachment) []Attachment {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func removeProp(xs []Propagation, x Propagation) []Propagation {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func removeBlock(xs []Block, x Block) []Block {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func removeGrant(xs []Grant, x Grant) []Grant {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
