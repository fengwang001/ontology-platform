package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// 本文件包含一个与 Gateway 判定引擎相互独立的朴素参考实现：
// 它直接枚举所有简单传播路径并逐条计算结果再取并集。随机生成的
// 链接图与授权序列下，两者的最终判定必须完全一致。

// naiveModel 是朴素图遍历参考模型。
type naiveModel struct {
	maxDepth  int
	links     []LinkType
	overrides map[ObjectTypeID]OverrideMode
	grants    map[SubjectID]map[ObjectTypeID]map[Action]grantValue
}

func (m *naiveModel) allows(sub SubjectID, typ ObjectTypeID) []Action {
	var out []Action
	for a, v := range m.grants[sub][typ] {
		if v == grantAllow {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// naiveDecide 枚举从每个授权起点出发的全部简单路径，逐条应用
// 深度与覆盖规则，最后在目标处取并集。
func (m *naiveModel) naiveDecide(sub SubjectID, target ObjectTypeID) (allowed []Action, reason ReasonCode) {
	explicitAllow, explicitDeny := map[Action]bool{}, map[Action]bool{}
	for a, v := range m.grants[sub][target] {
		if v == grantAllow {
			explicitAllow[a] = true
		} else {
			explicitDeny[a] = true
		}
	}

	propagated := map[Action]bool{}
	depthLimited := false

	var sources []ObjectTypeID
	for typ, actions := range m.grants[sub] {
		for _, v := range actions {
			if v == grantAllow {
				sources = append(sources, typ)
				break
			}
		}
	}

	for _, src := range sources {
		// 每条路径独立携带 (来源类型, 剩余深度)，按简单路径枚举。
		visited := map[ObjectTypeID]bool{src: true}
		var walk func(cur, origin ObjectTypeID, rem int)
		walk = func(cur, origin ObjectTypeID, rem int) {
			// 到达目标：按覆盖前的上游来源记录贡献。
			if cur == target {
				for _, a := range m.allows(sub, origin) {
					propagated[a] = true
				}
			}
			switch m.overrides[cur] {
			case OverrideReplaceAndBlock:
				return // 路径在此被阻断
			case OverrideReplace:
				origin = cur
			}
			if len(m.allows(sub, origin)) == 0 {
				return
			}
			if rem == 0 {
				for _, l := range m.links {
					if l.From == cur && l.participates() && l.To == target {
						depthLimited = true
					}
				}
				return
			}
			for _, l := range m.links {
				if l.From != cur || !l.participates() || visited[l.To] {
					continue
				}
				if r := min(rem, l.MaxDepth) - 1; r >= 0 {
					visited[l.To] = true
					walk(l.To, origin, r)
					delete(visited, l.To)
				}
			}
		}
		walk(src, src, m.maxDepth)
	}

	if m.overrides[target] != OverrideNone {
		propagated = map[Action]bool{}
	}

	final := map[Action]bool{}
	for a := range explicitAllow {
		final[a] = true
	}
	for a := range propagated {
		final[a] = true
	}
	for a := range explicitDeny {
		delete(final, a)
	}
	for a := range final {
		allowed = append(allowed, a)
	}
	sort.Slice(allowed, func(i, j int) bool { return allowed[i] < allowed[j] })

	switch {
	case len(allowed) > 0:
		reason = ReasonGranted
	case len(explicitDeny) > 0:
		reason = ReasonExplicitDeny
	case m.overrides[target] == OverrideReplaceAndBlock:
		reason = ReasonOverrideBlocked
	case m.overrides[target] == OverrideReplace:
		reason = ReasonOverrideReplaced
	case depthLimited:
		reason = ReasonDepthExhausted
	default:
		reason = ReasonNoGrant
	}
	return allowed, reason
}

// TestAgainstNaiveModel 在随机生成的链接图（按拓扑序加边，保证
// 传播子图无环）与随机授权序列下，逐条对照优化引擎与朴素模型
// 的最终判定，并打印每次判定的输入、输出与依据。
func TestAgainstNaiveModel(t *testing.T) {
	const (
		types     = 9
		actions   = 3
		rounds    = 200
		queries   = 6
		maxDepth  = 4
		edgeProb  = 0.30
		ovrProb   = 0.25
		grantProb = 0.40
		denyProb  = 0.15
	)
	rng := rand.New(rand.NewSource(20261007))
	acts := []Action{"read", "write", "admin"}

	for round := 0; round < rounds; round++ {
		g := NewGateway(Config{MaxPropagationDepth: maxDepth})
		model := &naiveModel{
			maxDepth:  maxDepth,
			overrides: map[ObjectTypeID]OverrideMode{},
			grants:    map[SubjectID]map[ObjectTypeID]map[Action]grantValue{},
		}
		ids := make([]ObjectTypeID, types)
		for i := range ids {
			ids[i] = ObjectTypeID(fmt.Sprintf("T%d", i))
			g.AddObjectType(ids[i])
		}
		// 随机 DAG：仅 i<j 加边。
		linkN := 0
		for i := 0; i < types; i++ {
			for j := i + 1; j < types; j++ {
				if rng.Float64() >= edgeProb {
					continue
				}
				depth := rng.Intn(maxDepth) // 0..maxDepth-1，含不传播的 0
				lt := LinkType{
					ID:         LinkTypeID(fmt.Sprintf("L%d", linkN)),
					From:       ids[i],
					To:         ids[j],
					Propagates: depth > 0,
					MaxDepth:   depth,
				}
				linkN++
				if err := g.AddLinkType(lt); err != nil {
					t.Fatalf("round %d: AddLinkType: %v", round, err)
				}
				model.links = append(model.links, lt)
			}
		}
		// 随机覆盖规则。
		for _, id := range ids {
			if rng.Float64() < ovrProb {
				mode := OverrideReplace
				if rng.Intn(2) == 0 {
					mode = OverrideReplaceAndBlock
				}
				if err := g.SetOverride(id, mode); err != nil {
					t.Fatalf("round %d: SetOverride: %v", round, err)
				}
				model.overrides[id] = mode
			}
		}
		// 随机授权序列（允许与显式否定交织）。
		sub := SubjectID("s")
		for _, id := range ids {
			for _, a := range acts[:actions] {
				switch r := rng.Float64(); {
				case r < grantProb:
					if err := g.Grant(sub, id, a); err != nil {
						t.Fatalf("round %d: Grant: %v", round, err)
					}
					putGrant(model.grants, sub, id, a, grantAllow)
				case r < grantProb+denyProb:
					if err := g.Deny(sub, id, a); err != nil {
						t.Fatalf("round %d: Deny: %v", round, err)
					}
					putGrant(model.grants, sub, id, a, grantDeny)
				}
			}
		}
		// 随机撤销一部分，模拟授权变更序列。
		for _, id := range ids {
			a := acts[rng.Intn(actions)]
			if err := g.Revoke(sub, id, a); err != nil {
				t.Fatalf("round %d: Revoke: %v", round, err)
			}
			if m := model.grants[sub]; m != nil {
				delete(m[id], a)
			}
		}

		for q := 0; q < queries; q++ {
			target := ids[rng.Intn(types)]
			dec, err := g.Decide(sub, target)
			wantAllowed, wantReason := model.naiveDecide(sub, target)
			logDecision(t, sub, target, dec, err)

			if wantReason == ReasonGranted {
				if err != nil {
					t.Fatalf("round %d target %s: engine err=%v, model grants %v",
						round, target, err, wantAllowed)
				}
			} else if !errors.Is(err, ErrNoPermission) {
				t.Fatalf("round %d target %s: engine err=%v, model reason=%s",
					round, target, err, wantReason)
			}
			if !equalActions(dec.Allowed, wantAllowed) {
				t.Fatalf("round %d target %s: engine allowed=%v, model=%v",
					round, target, dec.Allowed, wantAllowed)
			}
			if dec.Reason != wantReason {
				t.Fatalf("round %d target %s: engine reason=%s, model=%s",
					round, target, dec.Reason, wantReason)
			}
		}
	}
}

func putGrant(m map[SubjectID]map[ObjectTypeID]map[Action]grantValue,
	sub SubjectID, typ ObjectTypeID, a Action, v grantValue) {
	if m[sub] == nil {
		m[sub] = map[ObjectTypeID]map[Action]grantValue{}
	}
	if m[sub][typ] == nil {
		m[sub][typ] = map[Action]grantValue{}
	}
	m[sub][typ][a] = v
}

// equalActions 比较两个已升序排序的动作集合，nil 与空切片等价。
func equalActions(a, b []Action) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
