package ontology

import "sort"

// DecisionBasis 记录一次覆盖判定的依据，用于审计与测试核对。
type DecisionBasis struct {
	// Layer 给出最终判定的层。
	Layer Layer `json:"layer"`
	// LinkType 链接类型层判定时涉及的链接类型。
	LinkType LinkTypeID `json:"linkType,omitempty"`
	// ObjectType 对象类型层判定时涉及的对象类型。
	ObjectType ObjectTypeID `json:"objectType,omitempty"`
	// Priority 起决定作用的优先级数值。
	Priority int `json:"priority"`
	// Groups 在该优先级上给出声明的权限组，按标识排序。
	Groups []GroupID `json:"groups,omitempty"`
	// Values 各权限组给出的声明取值，与 Groups 一一对应。
	Values []DeclValue `json:"values,omitempty"`
	// Merged 该层合并后的判定结果。
	Merged Decision `json:"merged"`
}

// LinkDecision 记录对一条候选链接的完整判定过程。
type LinkDecision struct {
	// Link 被判定的候选链接。
	Link Link
	// Decision 最终判定结果。
	Decision Decision
	// Bases 判定依据：链接类型层一条，或对象类型层至多两条，或默认拒绝。
	Bases []DecisionBasis
}

// queryMetric 内部度量，只统计本次查询实际执行的覆盖规则计算次数
// （缓存命中不计），不对调用者暴露，仅供包内测试验证。
type queryMetric struct {
	linkTypeEvals   int
	objectTypeEvals int
}

// layerResult 是单层判定的输出。
type layerResult struct {
	declared bool
	decision Decision
	basis    DecisionBasis
}

// evalKey 是查询内判定缓存的键，主体在一次查询内固定。
type evalKey struct {
	layer  Layer
	target string
}

// evaluator 在一次查询内基于固定快照执行覆盖判定。
// 快照不可变，因此按（层, 目标）缓存判定结果是安全的；
// 度量只统计缓存未命中、真正执行覆盖规则计算的次数。
type evaluator struct {
	snap      *snapshot
	policy    ConflictPolicy
	principal PrincipalID
	groups    []GroupID
	memo      map[evalKey]layerResult
	metric    *queryMetric
	// hook 仅供包内测试在判定过程中插入同步点。
	hook func()
}

// newEvaluator 基于给定快照为指定主体创建判定器。
func newEvaluator(snap *snapshot, policy ConflictPolicy, principal PrincipalID, metric *queryMetric) *evaluator {
	e := &evaluator{
		snap:      snap,
		policy:    policy,
		principal: principal,
		memo:      make(map[evalKey]layerResult),
		metric:    metric,
	}
	for g := range snap.members[principal] {
		e.groups = append(e.groups, g)
	}
	sort.Slice(e.groups, func(i, j int) bool { return e.groups[i] < e.groups[j] })
	return e
}

// mergeValues 按合并策略合并同一优先级上的声明取值集合。
// 返回合并结果；ok 为 false 表示合并规则无法给出单一结果（歧义）。
func mergeValues(values []DeclValue, policy ConflictPolicy) (Decision, bool) {
	hasAllow, hasDeny := false, false
	for _, v := range values {
		switch v {
		case DeclAllow:
			hasAllow = true
		case DeclDeny:
			hasDeny = true
		}
	}
	switch {
	case hasAllow && hasDeny:
		// 声明矛盾：默认策略拒绝优先；严格策略无法给出单一结果。
		if policy == StrictConflict {
			return DecisionAmbiguous, false
		}
		return DecisionDeny, true
	case hasDeny:
		return DecisionDeny, true
	case hasAllow:
		return DecisionAllow, true
	default:
		return DecisionAmbiguous, false
	}
}

// evalLayer 在指定层上评估主体对单个目标的准入。
// 只考虑主体所属权限组中对该目标有声明的组，取优先级数值最高的一层，
// 同优先级多组声明按合并策略合并。
func (e *evaluator) evalLayer(layer Layer, target string, decls func(*groupDecls) (DeclValue, bool)) layerResult {
	key := evalKey{layer: layer, target: target}
	if res, ok := e.memo[key]; ok {
		return res
	}
	if e.hook != nil {
		e.hook()
	}
	if layer == LayerLink {
		e.metric.linkTypeEvals++
	} else {
		e.metric.objectTypeEvals++
	}

	bestPriority := 0
	found := false
	var groups []GroupID
	var values []DeclValue
	for _, gid := range e.groups {
		g := e.snap.groups[gid]
		if g == nil {
			continue
		}
		v, ok := decls(g)
		if !ok {
			continue
		}
		if !found || g.priority > bestPriority {
			found = true
			bestPriority = g.priority
			groups = []GroupID{gid}
			values = []DeclValue{v}
		} else if g.priority == bestPriority {
			groups = append(groups, gid)
			values = append(values, v)
		}
	}

	res := layerResult{}
	if !found {
		res.declared = false
	} else {
		merged, ok := mergeValues(values, e.policy)
		res.declared = true
		if ok {
			res.decision = merged
		} else {
			res.decision = DecisionAmbiguous
		}
		res.basis = DecisionBasis{
			Layer:    layer,
			Priority: bestPriority,
			Groups:   groups,
			Values:   values,
			Merged:   res.decision,
		}
		if layer == LayerLink {
			res.basis.LinkType = LinkTypeID(target)
		} else {
			res.basis.ObjectType = ObjectTypeID(target)
		}
	}
	e.memo[key] = res
	return res
}

// evalLinkType 评估链接类型层对指定链接类型的准入。
func (e *evaluator) evalLinkType(linkType LinkTypeID) layerResult {
	return e.evalLayer(LayerLink, string(linkType), func(g *groupDecls) (DeclValue, bool) {
		v, ok := g.linkLayer[linkType]
		return v, ok
	})
}

// evalObjectType 评估对象类型层对指定对象类型的准入。
func (e *evaluator) evalObjectType(objType ObjectTypeID) layerResult {
	return e.evalLayer(LayerObject, string(objType), func(g *groupDecls) (DeclValue, bool) {
		v, ok := g.objectLayer[objType]
		return v, ok
	})
}

// authorize 按覆盖规则判定一条候选链接是否允许遍历。
// 判定次序：链接类型层明确声明 > 对象类型层两端合并（拒绝优先）> 默认拒绝。
// 任何一层出现歧义即整体歧义。
func (e *evaluator) authorize(link Link, fromType, toType ObjectTypeID) LinkDecision {
	out := LinkDecision{Link: link}

	linkRes := e.evalLinkType(link.Type)
	if linkRes.declared {
		out.Decision = linkRes.decision
		out.Bases = []DecisionBasis{linkRes.basis}
		return out
	}

	fromRes := e.evalObjectType(fromType)
	toRes := e.evalObjectType(toType)
	switch {
	case fromRes.declared && toRes.declared:
		out.Bases = []DecisionBasis{fromRes.basis, toRes.basis}
		out.Decision = mergeEndpointDecisions(fromRes.decision, toRes.decision)
	case fromRes.declared:
		out.Bases = []DecisionBasis{fromRes.basis}
		out.Decision = fromRes.decision
	case toRes.declared:
		out.Bases = []DecisionBasis{toRes.basis}
		out.Decision = toRes.decision
	default:
		out.Bases = []DecisionBasis{{Layer: LayerDefault, Merged: DecisionDeny}}
		out.Decision = DecisionDeny
	}
	return out
}

// mergeEndpointDecisions 合并两端对象类型的判定：歧义传播，否则拒绝优先。
func mergeEndpointDecisions(a, b Decision) Decision {
	if a == DecisionAmbiguous || b == DecisionAmbiguous {
		return DecisionAmbiguous
	}
	if a == DecisionDeny || b == DecisionDeny {
		return DecisionDeny
	}
	return DecisionAllow
}
