package ontology

import (
	"sort"
)

// Contribution 记录一条到达目标的传播贡献，用于解释判定依据。
type Contribution struct {
	// Origin 是携带权限集合的来源对象类型：要么是主体持有直接
	// 授权的起点类型，要么是声明了 OverrideReplace 的覆盖类型
	// （穿越它之后携带的集合被替换为该类型的直接授权）。
	Origin ObjectTypeID
	// Remaining 是到达目标时剩余的传播深度。
	Remaining int
	// Actions 是该贡献实际带给目标的权限动作集合。
	Actions []Action
}

// Stats 记录一次判定考察的工作量，用于验证性能要求：
// 考察量只取决于从授权起点可达的传播子图，与平台内链接类型
// 总数、对象类型总数无关。
type Stats struct {
	// EdgesExamined 是判定过程中考察的（链接类型, 状态）松弛次数。
	EdgesExamined int
	// StatesVisited 是实际处理的不同（类型, 来源, 剩余深度）状态数。
	StatesVisited int
}

// Decision 是一次权限判定的完整结果。
type Decision struct {
	Subject SubjectID
	Target  ObjectTypeID
	// Allowed 是最终生效的权限动作集合（升序、去重）。
	Allowed []Action
	// Denied 是目标类型上针对该主体的显式否定项（升序、去重）。
	Denied []Action
	// Reason 解释结论依据；无权限时按拒绝优先级给出首要原因。
	Reason ReasonCode
	// Contributions 是到达目标的各条传播贡献（覆盖生效时被丢弃，
	// 但仍记录在此以便审计）。
	Contributions []Contribution
	// Notes 是判定过程中产生的人类可读说明（覆盖、阻断、深度
	// 耗尽等）。
	Notes []string
	// Stats 是本次判定考察的工作量。
	Stats Stats
}

// Decide 判定主体对目标对象类型的最终权限。
//
// 错误约定：目标对象类型不存在返回 ErrObjectTypeNotFound；传播配置
// 含环路返回 ErrPropagationCycle；二者之外的“无权限”不是配置性
// 错误，返回 ErrNoPermission 并附带完整的 Decision（Reason 区分
// 显式拒绝、覆盖阻断与深度自然耗尽）。
func (g *Gateway) Decide(subject SubjectID, target ObjectTypeID) (Decision, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.decideLocked(subject, target)
}

// decideLocked 是 Decide 的内部实现，调用方须已持有读锁。
func (g *Gateway) decideLocked(subject SubjectID, target ObjectTypeID) (Decision, error) {
	dec := Decision{Subject: subject, Target: target, Reason: ReasonNoGrant}

	if _, ok := g.objectTypes[target]; !ok {
		return dec, ErrObjectTypeNotFound
	}

	// 防御性环路检测：只考察从授权起点可达的传播子图。正常情况
	// 下写路径已拒绝成环的配置，这里保证即便状态被绕过写路径破
	// 坏，判定也不会沿环静默传播，而是报“传播环路”错误。
	sources := g.grantSources(subject)
	if g.hasCycleFrom(sources) {
		return dec, ErrPropagationCycle
	}

	propagated, depthLimited := g.propagate(subject, target, sources, &dec)

	explicitAllow, explicitDeny := g.explicitGrants(subject, target)
	dec.Denied = explicitDeny

	// 目标类型声明覆盖时，传播结果的并集被整体替换（而非合并）。
	if mode := g.overrides[target]; mode != OverrideNone {
		if len(propagated) > 0 {
			dec.Notes = append(dec.Notes,
				"target declares "+mode.String()+" override: propagated union discarded")
		}
		propagated = nil
	}

	// 显式授权（含显式否定项）始终优先于传播结果。
	allowed := union(explicitAllow, propagated)
	allowed = subtract(allowed, explicitDeny)
	dec.Allowed = allowed
	dec.Notes = dedupeNotes(dec.Notes)

	if len(allowed) > 0 {
		dec.Reason = ReasonGranted
		return dec, nil
	}
	// 拒绝优先级：显式拒绝 > 覆盖阻断 > 覆盖替换 > 深度耗尽 > 无授权。
	switch {
	case len(explicitDeny) > 0:
		dec.Reason = ReasonExplicitDeny
	case g.overrides[target] == OverrideReplaceAndBlock:
		dec.Reason = ReasonOverrideBlocked
	case g.overrides[target] == OverrideReplace:
		dec.Reason = ReasonOverrideReplaced
	case depthLimited:
		dec.Reason = ReasonDepthExhausted
	default:
		dec.Reason = ReasonNoGrant
	}
	return dec, ErrNoPermission
}

// dedupeNotes 去除判定过程中重复产生的说明（同一覆盖类型可能
// 被多条路径经过），保持首次出现的顺序。
func dedupeNotes(notes []string) []string {
	seen := make(map[string]struct{}, len(notes))
	out := notes[:0]
	for _, n := range notes {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// grantSources 返回主体持有显式允许项的全部对象类型（升序），
// 作为传播的起点集合。
func (g *Gateway) grantSources(subject SubjectID) []ObjectTypeID {
	var sources []ObjectTypeID
	for typ, actions := range g.grants[subject] {
		for _, v := range actions {
			if v == grantAllow {
				sources = append(sources, typ)
				break
			}
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	return sources
}

// explicitGrants 返回主体在目标类型上的显式允许项与显式否定项。
func (g *Gateway) explicitGrants(subject SubjectID, target ObjectTypeID) (allow, deny []Action) {
	for a, v := range g.grants[subject][target] {
		switch v {
		case grantAllow:
			allow = append(allow, a)
		case grantDeny:
			deny = append(deny, a)
		}
	}
	sortActions(allow)
	sortActions(deny)
	return allow, deny
}

// directAllows 返回主体在某类型上的显式允许动作集合（升序）。
func (g *Gateway) directAllows(subject SubjectID, typ ObjectTypeID) []Action {
	var out []Action
	for a, v := range g.grants[subject][typ] {
		if v == grantAllow {
			out = append(out, a)
		}
	}
	sortActions(out)
	return out
}

// state 是传播遍历的一个节点：当前类型、携带集合的来源类型、
// 到达时剩余的传播深度。
type state struct {
	cur    ObjectTypeID
	origin ObjectTypeID
	rem    int
}

// propagate 从授权起点出发沿参与传播的链接类型扩散，返回到达
// target 的权限并集，以及是否存在“再有一跳深度即可到达 target”
// 的深度自然耗尽现象。途中把贡献与说明写入 dec。
func (g *Gateway) propagate(subject SubjectID, target ObjectTypeID, sources []ObjectTypeID, dec *Decision) ([]Action, bool) {
	// best[cur][origin] 记录已处理的最大剩余深度；状态数被
	// （可达类型数 × 来源数 × 深度上限）约束，与平台总规模无关。
	best := make(map[ObjectTypeID]map[ObjectTypeID]int)
	arrivals := make(map[ObjectTypeID]int) // origin -> 到达 target 时的最大剩余深度
	depthLimited := false

	queue := make([]state, 0, len(sources))
	for _, s := range sources {
		queue = append(queue, state{cur: s, origin: s, rem: g.maxDepth})
	}

	for len(queue) > 0 {
		st := queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		if m, ok := best[st.cur]; ok {
			if b, seen := m[st.origin]; seen && b >= st.rem {
				continue
			}
		}
		if best[st.cur] == nil {
			best[st.cur] = make(map[ObjectTypeID]int)
		}
		best[st.cur][st.origin] = st.rem
		dec.Stats.StatesVisited++

		// 先按上游来源记录到达目标的贡献（若目标声明覆盖，该并集
		// 稍后被整体替换，但贡献仍保留在判定依据中以便审计）。
		if st.cur == target {
			if carried := g.directAllows(subject, st.origin); len(carried) > 0 {
				if prev, ok := arrivals[st.origin]; !ok || st.rem > prev {
					arrivals[st.origin] = st.rem
				}
			}
		}

		// 携带的权限集合 = 来源类型的直接授权；穿越 OverrideReplace
		// 类型后来源被替换为该类型自身。
		origin := st.origin
		switch g.overrides[st.cur] {
		case OverrideReplaceAndBlock:
			dec.Notes = append(dec.Notes,
				"propagation blocked at "+string(st.cur)+" (replace-and-block override)")
			continue
		case OverrideReplace:
			origin = st.cur
			dec.Notes = append(dec.Notes,
				"propagated set replaced by direct grants of "+string(st.cur))
		}

		carried := g.directAllows(subject, origin)
		if len(carried) == 0 {
			continue
		}
		if st.rem == 0 {
			// 深度自然耗尽：不是错误。记录是否再有一跳即可到达目标。
			for _, l := range g.linkTypes {
				if l.From == st.cur && l.participates() && l.To == target {
					depthLimited = true
				}
			}
			continue
		}
		for _, l := range g.linkTypes {
			if l.From != st.cur || !l.participates() {
				continue
			}
			dec.Stats.EdgesExamined++
			// 剩余深度恰为零的那一跳仍允许生效。
			r := min(st.rem, l.MaxDepth) - 1
			if r >= 0 {
				queue = append(queue, state{cur: l.To, origin: origin, rem: r})
			}
		}
	}

	var out []Action
	origins := make([]ObjectTypeID, 0, len(arrivals))
	for o := range arrivals {
		origins = append(origins, o)
	}
	sort.Slice(origins, func(i, j int) bool { return origins[i] < origins[j] })
	for _, o := range origins {
		actions := g.directAllows(subject, o)
		dec.Contributions = append(dec.Contributions, Contribution{
			Origin:    o,
			Remaining: arrivals[o],
			Actions:   actions,
		})
		out = union(out, actions)
	}
	return out, depthLimited
}

// hasCycleFrom 检测从给定起点集合出发、由参与传播的链接类型构成
// 的有向子图中是否存在环路（三色 DFS）。
func (g *Gateway) hasCycleFrom(sources []ObjectTypeID) bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[ObjectTypeID]int)

	adj := make(map[ObjectTypeID][]ObjectTypeID)
	for _, l := range g.linkTypes {
		if l.participates() {
			adj[l.From] = append(adj[l.From], l.To)
		}
	}

	var visit func(u ObjectTypeID) bool
	visit = func(u ObjectTypeID) bool {
		color[u] = gray
		for _, v := range adj[u] {
			switch color[v] {
			case gray:
				return true
			case white:
				if visit(v) {
					return true
				}
			}
		}
		color[u] = black
		return false
	}
	for _, s := range sources {
		if color[s] == white && visit(s) {
			return true
		}
	}
	return false
}

func union(a, b []Action) []Action {
	if len(a) == 0 {
		return b
	}
	seen := make(map[Action]struct{}, len(a)+len(b))
	out := make([]Action, 0, len(a)+len(b))
	for _, x := range a {
		seen[x] = struct{}{}
		out = append(out, x)
	}
	for _, x := range b {
		if _, ok := seen[x]; !ok {
			seen[x] = struct{}{}
			out = append(out, x)
		}
	}
	sortActions(out)
	return out
}

func subtract(a, b []Action) []Action {
	if len(b) == 0 {
		return a
	}
	ban := make(map[Action]struct{}, len(b))
	for _, x := range b {
		ban[x] = struct{}{}
	}
	out := a[:0]
	for _, x := range a {
		if _, ok := ban[x]; !ok {
			out = append(out, x)
		}
	}
	return out
}

func sortActions(a []Action) {
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
}
