package ontology

import "sort"

// NaiveEngine 是独立维护的朴素参照实现：不做任何物化与增量维护，
// 每次查询都直接从原始声明出发枚举简单路径、递归角色层级。
// 它刻意采用与 Engine 不同的算法（路径枚举 vs 不动点迭代），
// 用于在随机化差异测试中逐项对照 Engine 的判定结果。
type NaiveEngine struct {
	decl *declarations
}

// NewNaive 创建一个空的朴素参照引擎。
func NewNaive() *NaiveEngine {
	return &NaiveEngine{decl: newDeclarations()}
}

// --- 与 Engine 对应的变更 API（不做校验，由测试保证操作合法） ---

func (n *NaiveEngine) AddObjectType(id string) error { n.decl.objectTypes[id] = true; return nil }
func (n *NaiveEngine) AddLinkType(id string) error   { n.decl.linkTypes[id] = true; return nil }
func (n *NaiveEngine) AddTag(id string) error        { n.decl.tags[id] = true; return nil }

func (n *NaiveEngine) AddLinkEdge(e LinkEdge) error    { n.decl.edges[e] = true; return nil }
func (n *NaiveEngine) RemoveLinkEdge(e LinkEdge) error { delete(n.decl.edges, e); return nil }
func (n *NaiveEngine) AttachTag(a Attachment) error    { n.decl.attachments[a] = true; return nil }
func (n *NaiveEngine) DetachTag(a Attachment) error    { delete(n.decl.attachments, a); return nil }

func (n *NaiveEngine) AddPropagation(p Propagation) error {
	n.decl.propagations[p] = true
	return nil
}

func (n *NaiveEngine) RemovePropagation(p Propagation) error {
	delete(n.decl.propagations, p)
	return nil
}

func (n *NaiveEngine) AddBlock(b Block) error    { n.decl.blocks[b] = true; return nil }
func (n *NaiveEngine) RemoveBlock(b Block) error { delete(n.decl.blocks, b); return nil }

func (n *NaiveEngine) AddRole(id string, parents ...string) error {
	if n.decl.roleParents[id] == nil {
		n.decl.roleParents[id] = map[string]bool{}
	}
	for _, p := range parents {
		n.decl.roleParents[id][p] = true
	}
	return nil
}

func (n *NaiveEngine) AddGrant(g Grant) error    { n.decl.grants[g] = true; return nil }
func (n *NaiveEngine) RemoveGrant(g Grant) error { delete(n.decl.grants, g); return nil }

func (n *NaiveEngine) AddSubject(id string, roles ...string) error {
	if n.decl.subjectRoles[id] == nil {
		n.decl.subjectRoles[id] = map[string]bool{}
	}
	for _, r := range roles {
		n.decl.subjectRoles[id][r] = true
	}
	return nil
}

func (n *NaiveEngine) AddInstance(id, objectType string) error {
	n.decl.instances[id] = objectType
	return nil
}

// reachable 通过枚举简单路径判断标签 tag 是否可从某个直接挂载源头
// 传播到对象类型 target。honorBlocks 为 true 时阻断点截断路径
// （阻断点自身可作为路径终点，但不能作为中间节点继续外传）。
// 路径不允许重复经过同一对象类型，因此关系图中的环不会导致不终止。
func (n *NaiveEngine) reachable(target, tag string, honorBlocks bool) bool {
	props := []Propagation{}
	for p := range n.decl.propagations {
		if p.Tag == tag {
			props = append(props, p)
		}
	}
	sort.Slice(props, func(i, j int) bool {
		if props[i].LinkType != props[j].LinkType {
			return props[i].LinkType < props[j].LinkType
		}
		return props[i].Direction < props[j].Direction
	})
	edges := make([]LinkEdge, 0, len(n.decl.edges))
	for e := range n.decl.edges {
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].LinkType != edges[j].LinkType {
			return edges[i].LinkType < edges[j].LinkType
		}
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	var dfs func(cur string, onPath map[string]bool) bool
	dfs = func(cur string, onPath map[string]bool) bool {
		if cur == target {
			return true
		}
		if honorBlocks && n.decl.blocks[Block{ObjectType: cur, Tag: tag}] {
			return false // 阻断点：不再向外传播
		}
		for _, p := range props {
			for _, e := range edges {
				if e.LinkType != p.LinkType {
					continue
				}
				var next string
				if p.Direction == Downstream {
					if e.From != cur {
						continue
					}
					next = e.To
				} else {
					if e.To != cur {
						continue
					}
					next = e.From
				}
				if onPath[next] {
					continue
				}
				onPath[next] = true
				if dfs(next, onPath) {
					return true
				}
				delete(onPath, next)
			}
		}
		return false
	}

	atts := make([]Attachment, 0, len(n.decl.attachments))
	for a := range n.decl.attachments {
		if a.Tag == tag {
			atts = append(atts, a)
		}
	}
	sort.Slice(atts, func(i, j int) bool { return atts[i].ObjectType < atts[j].ObjectType })
	for _, a := range atts {
		if dfs(a.ObjectType, map[string]bool{a.ObjectType: true}) {
			return true
		}
	}
	return false
}

// scan 递归遍历角色层级，收集允许/拒绝结论并检测继承环。
func (n *NaiveEngine) scan(subject, tag string) (hasAllow, hasDeny, cycle bool) {
	direct := n.decl.subjectRoles[subject]
	color := map[string]int{}
	var visit func(role string)
	visit = func(role string) {
		color[role] = 1
		for g := range n.decl.grants {
			if g.Role == role && g.Tag == tag {
				if g.Effect == Allow {
					hasAllow = true
				} else {
					hasDeny = true
				}
			}
		}
		parents := make([]string, 0, len(n.decl.roleParents[role]))
		for p := range n.decl.roleParents[role] {
			parents = append(parents, p)
		}
		sort.Strings(parents)
		for _, p := range parents {
			switch color[p] {
			case 1:
				cycle = true
			case 0:
				visit(p)
			}
		}
		color[role] = 2
	}
	roots := make([]string, 0, len(direct))
	for r := range direct {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	for _, r := range roots {
		if color[r] == 0 {
			visit(r)
		}
	}
	return hasAllow, hasDeny, cycle
}

// adjudicate 独立实现的裁决规则（与 Engine.adjudicate 规格相同）：
// 显式拒绝优先于允许；角色环在允许/拒绝之后汇报；无授权默认拒绝。
func adjudicateNaive(hasAllow, hasDeny, cycle bool) (bool, ReasonCode) {
	switch {
	case hasAllow && hasDeny:
		return false, ReasonDenyOverrides
	case hasDeny:
		return false, ReasonExplicitDeny
	case cycle:
		return false, ReasonRoleCycle
	case hasAllow:
		return true, ReasonAllowed
	default:
		return false, ReasonNoGrant
	}
}

// DecideTag 与 Engine.DecideTag 规格一致的朴素实现。
func (n *NaiveEngine) DecideTag(subject, instance, tag string) Decision {
	d := Decision{Subject: subject, Instance: instance, Tag: tag}
	if _, ok := n.decl.subjectRoles[subject]; !ok {
		d.Reason = ReasonNotFound
		return d
	}
	if !n.decl.tags[tag] {
		d.Reason = ReasonNotFound
		return d
	}
	objType, ok := n.decl.instances[instance]
	if !ok {
		d.Reason = ReasonNotFound
		return d
	}
	if !n.reachable(objType, tag, true) {
		if n.reachable(objType, tag, false) {
			d.Reason = ReasonAllPathsBlocked
			return d
		}
		d.Allowed, d.Reason = true, ReasonTagNotPresent
		return d
	}
	hasAllow, hasDeny, cycle := n.scan(subject, tag)
	d.Allowed, d.Reason = adjudicateNaive(hasAllow, hasDeny, cycle)
	return d
}

// Decide 与 Engine.Decide 规格一致的朴素实现：全允许才允许。
func (n *NaiveEngine) Decide(subject, instance string) Decision {
	d := Decision{Subject: subject, Instance: instance}
	if _, ok := n.decl.subjectRoles[subject]; !ok {
		d.Reason = ReasonNotFound
		return d
	}
	objType, ok := n.decl.instances[instance]
	if !ok {
		d.Reason = ReasonNotFound
		return d
	}
	tags := make([]string, 0, len(n.decl.tags))
	for tag := range n.decl.tags {
		if n.reachable(objType, tag, true) {
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	d.Allowed, d.Reason = true, ReasonAllowed
	bestRank := 1 << 30
	for _, tag := range tags {
		hasAllow, hasDeny, cycle := n.scan(subject, tag)
		allowed, reason := adjudicateNaive(hasAllow, hasDeny, cycle)
		if !allowed {
			d.Allowed = false
			if r := reasonRank(reason); r < bestRank {
				bestRank = r
				d.Reason = reason
			}
		}
	}
	return d
}
