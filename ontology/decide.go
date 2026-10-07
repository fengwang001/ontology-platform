package ontology

import (
	"fmt"
	"log/slog"
	"sort"
)

// roleScan 是对某主体在某标签上的角色层级扫描结果。
type roleScan struct {
	evidences []GrantEvidence
	hasAllow  bool
	hasDeny   bool
	cycle     bool // 可达角色层级中存在继承环
	nodes     int  // 遍历到的角色节点数
}

// scanRoles 从主体的全部直接角色出发遍历上级角色闭包，收集授权证据。
// 遍历使用三色标记检测继承环；环不会导致遍历不终止。
func scanRoles(decl *declarations, snap *snapshot, subject, tag string) roleScan {
	direct := decl.subjectRoles[subject]
	var scan roleScan
	color := map[string]int{} // 0=未访问 1=在栈上 2=已完成
	roots := make([]string, 0, len(direct))
	for r := range direct {
		roots = append(roots, r)
	}
	sort.Strings(roots)

	var visit func(role string)
	visit = func(role string) {
		color[role] = 1
		scan.nodes++
		for _, g := range snap.grantsByRole[role] {
			if g.Tag != tag {
				continue
			}
			scan.evidences = append(scan.evidences, GrantEvidence{
				Role:      g.Role,
				Tag:       g.Tag,
				Effect:    g.Effect,
				Inherited: !direct[g.Role],
			})
			if g.Effect == Allow {
				scan.hasAllow = true
			} else {
				scan.hasDeny = true
			}
		}
		parents := make([]string, 0, len(decl.roleParents[role]))
		for p := range decl.roleParents[role] {
			parents = append(parents, p)
		}
		sort.Strings(parents)
		for _, p := range parents {
			switch color[p] {
			case 1:
				scan.cycle = true
			case 0:
				visit(p)
			}
		}
		color[role] = 2
	}
	for _, r := range roots {
		if color[r] == 0 {
			visit(r)
		}
	}
	sort.Slice(scan.evidences, func(i, j int) bool {
		if scan.evidences[i].Role != scan.evidences[j].Role {
			return scan.evidences[i].Role < scan.evidences[j].Role
		}
		return scan.evidences[i].Effect < scan.evidences[j].Effect
	})
	return scan
}

// adjudicate 应用确定的裁决规则：显式拒绝优先于允许（deny-overrides），
// 覆盖允许/拒绝各自来自直接声明或继承声明的全部四种组合。
func adjudicate(scan roleScan, tag string, sources []TagSource) TagVerdict {
	v := TagVerdict{Tag: tag, Grants: scan.evidences, Sources: sources}
	switch {
	case scan.hasAllow && scan.hasDeny:
		v.Allowed, v.Reason = false, ReasonDenyOverrides
	case scan.hasDeny:
		v.Allowed, v.Reason = false, ReasonExplicitDeny
	case scan.cycle:
		v.Allowed, v.Reason = false, ReasonRoleCycle
	case scan.hasAllow:
		v.Allowed, v.Reason = true, ReasonAllowed
	default:
		v.Allowed, v.Reason = false, ReasonNoGrant
	}
	return v
}

// Decide 判定主体对实例的整体访问权限：对实例携带的全部标签逐一裁决，
// 采用“全允许才允许”的综合规则，任一标签被拒绝则整体拒绝；
// 多个标签出错时按固定优先级汇报，结果与标签记录顺序无关。
func (e *Engine) Decide(subject, instance string) Decision {
	e.mu.RLock()
	defer e.mu.RUnlock()
	d := Decision{Subject: subject, Instance: instance}

	if _, ok := e.decl.subjectRoles[subject]; !ok {
		return e.finish(d, false, ReasonNotFound, fmt.Sprintf("主体 %q 不存在", subject))
	}
	objType, ok := e.decl.instances[instance]
	if !ok {
		return e.finish(d, false, ReasonNotFound, fmt.Sprintf("实例 %q 不存在", instance))
	}

	tags := make([]string, 0, len(e.snap.carried[objType]))
	for tag := range e.snap.carried[objType] {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	for _, tag := range tags {
		sources := e.snap.carried[objType][tag]
		scan := scanRoles(e.decl, e.snap, subject, tag)
		d.Verdicts = append(d.Verdicts, adjudicate(scan, tag, sources))
		d.Stats.TagSourcesVisited += len(sources)
		d.Stats.RoleNodesVisited += scan.nodes
	}

	for _, v := range d.Verdicts {
		if !v.Allowed {
			return e.finish(d, false, v.Reason,
				fmt.Sprintf("标签 %q 被拒绝（%s）", v.Tag, v.Reason))
		}
	}
	return e.finish(d, true, ReasonAllowed, "实例携带的全部标签均被允许")
}

// DecideTag 判定主体对实例在指定标签维度上的访问权限。
func (e *Engine) DecideTag(subject, instance, tag string) Decision {
	e.mu.RLock()
	defer e.mu.RUnlock()
	d := Decision{Subject: subject, Instance: instance, Tag: tag}

	if _, ok := e.decl.subjectRoles[subject]; !ok {
		return e.finish(d, false, ReasonNotFound, fmt.Sprintf("主体 %q 不存在", subject))
	}
	if !e.decl.tags[tag] {
		return e.finish(d, false, ReasonNotFound, fmt.Sprintf("标签 %q 不存在", tag))
	}
	objType, ok := e.decl.instances[instance]
	if !ok {
		return e.finish(d, false, ReasonNotFound, fmt.Sprintf("实例 %q 不存在", instance))
	}

	sources := e.snap.carried[objType][tag]
	if len(sources) == 0 {
		if e.snap.shadow[objType][tag] {
			return e.finish(d, false, ReasonAllPathsBlocked,
				fmt.Sprintf("标签 %q 到对象类型 %q 的全部继承路径均被阻断", tag, objType))
		}
		return e.finish(d, true, ReasonTagNotPresent,
			fmt.Sprintf("标签 %q 未附着于实例 %q，不构成约束", tag, instance))
	}

	scan := scanRoles(e.decl, e.snap, subject, tag)
	v := adjudicate(scan, tag, sources)
	d.Verdicts = []TagVerdict{v}
	d.Stats.TagSourcesVisited += len(sources)
	d.Stats.RoleNodesVisited += scan.nodes
	return e.finish(d, v.Allowed, v.Reason, fmt.Sprintf("标签 %q 裁决为 %s", tag, v.Reason))
}

// finish 汇总多标签裁决（如有），按固定优先级选出最终结论并写日志。
// 判定只读取状态，不修改标签继承、角色层级或任何时钟。
func (e *Engine) finish(d Decision, allowed bool, reason ReasonCode, detail string) Decision {
	if len(d.Verdicts) > 1 && !allowed {
		// 多标签场景：按固定优先级（数值小者优先）选出汇报原因，
		// 同优先级时取标签名字典序最小者，与标签记录顺序无关。
		best := d.Verdicts[0]
		for _, v := range d.Verdicts[1:] {
			if v.Allowed {
				continue
			}
			if best.Allowed || reasonRank(v.Reason) < reasonRank(best.Reason) {
				best = v
			}
		}
		reason = best.Reason
		detail = fmt.Sprintf("标签 %q 被拒绝（%s）", best.Tag, best.Reason)
	}
	d.Allowed, d.Reason, d.Detail = allowed, reason, detail
	e.logger.Info("ontology decide",
		slog.String("subject", d.Subject),
		slog.String("instance", d.Instance),
		slog.String("tag", d.Tag),
		slog.Bool("allowed", d.Allowed),
		slog.String("reason", string(d.Reason)),
		slog.String("detail", d.Detail),
		slog.Any("verdicts", d.Verdicts),
		slog.Int("tag_sources_visited", d.Stats.TagSourcesVisited),
		slog.Int("role_nodes_visited", d.Stats.RoleNodesVisited),
	)
	return d
}
