package ontology

import "sort"

// 本文件是独立于产品代码的朴素参考实现，仅用于测试对照。
// 它直接按规范文字逐层求值：不使用快照、缓存、索引等任何优化，
// 路径搜索通过枚举全部简单路径完成。

// naiveState 是权限状态的朴素表示。
type naiveState struct {
	policy      ConflictPolicy
	priorities  map[GroupID]int
	objectDecls map[GroupID]map[ObjectTypeID]DeclValue
	linkDecls   map[GroupID]map[LinkTypeID]DeclValue
	members     map[PrincipalID]map[GroupID]bool
}

func newNaiveState(policy ConflictPolicy) *naiveState {
	return &naiveState{
		policy:      policy,
		priorities:  make(map[GroupID]int),
		objectDecls: make(map[GroupID]map[ObjectTypeID]DeclValue),
		linkDecls:   make(map[GroupID]map[LinkTypeID]DeclValue),
		members:     make(map[PrincipalID]map[GroupID]bool),
	}
}

// naiveMerge 朴素合并同一优先级上的声明取值。
func naiveMerge(values []DeclValue, policy ConflictPolicy) Decision {
	hasAllow, hasDeny := false, false
	for _, v := range values {
		if v == DeclAllow {
			hasAllow = true
		}
		if v == DeclDeny {
			hasDeny = true
		}
	}
	switch {
	case hasAllow && hasDeny:
		if policy == StrictConflict {
			return DecisionAmbiguous
		}
		return DecisionDeny
	case hasDeny:
		return DecisionDeny
	case hasAllow:
		return DecisionAllow
	default:
		return DecisionAmbiguous
	}
}

// naiveEvalLayer 朴素地评估单层单目标：扫描主体全部权限组，
// 取有声明组中的最高优先级，合并该优先级上的全部声明。
func (st *naiveState) naiveEvalLayer(principal PrincipalID, linkLayer bool, target string) (bool, Decision) {
	groups := st.members[principal]
	best := 0
	found := false
	var values []DeclValue
	for g := range groups {
		var v DeclValue
		var ok bool
		if linkLayer {
			v, ok = st.linkDecls[g][LinkTypeID(target)]
		} else {
			v, ok = st.objectDecls[g][ObjectTypeID(target)]
		}
		if !ok {
			continue
		}
		p := st.priorities[g]
		if !found || p > best {
			found, best = true, p
			values = []DeclValue{v}
		} else if p == best {
			values = append(values, v)
		}
	}
	if !found {
		return false, DecisionDeny
	}
	return true, naiveMerge(values, st.policy)
}

// naiveAuthorize 朴素地按覆盖次序判定一条链接。
func (st *naiveState) naiveAuthorize(principal PrincipalID, link Link, fromType, toType ObjectTypeID) Decision {
	if declared, d := st.naiveEvalLayer(principal, true, string(link.Type)); declared {
		return d
	}
	fromDeclared, fromD := st.naiveEvalLayer(principal, false, string(fromType))
	toDeclared, toD := st.naiveEvalLayer(principal, false, string(toType))
	switch {
	case fromDeclared && toDeclared:
		if fromD == DecisionAmbiguous || toD == DecisionAmbiguous {
			return DecisionAmbiguous
		}
		if fromD == DecisionDeny || toD == DecisionDeny {
			return DecisionDeny
		}
		return DecisionAllow
	case fromDeclared:
		return fromD
	case toDeclared:
		return toD
	default:
		return DecisionDeny
	}
}

// naiveGraph 是图的朴素表示。
type naiveGraph struct {
	types map[ObjectID]ObjectTypeID
	adj   map[ObjectID][]Link
}

// naiveResult 是朴素路径搜索的结果。
type naiveResult struct {
	status QueryStatus
	path   []ObjectID
	cost   int64
}

// naiveShortestPath 枚举全部简单路径，返回（总代价, 对象标识序列）
// 最小的可遍历路径；任一遍历中遇到的链接判定为歧义则整体歧义。
func (st *naiveState) naiveShortestPath(principal PrincipalID, g *naiveGraph, from, to ObjectID) naiveResult {
	if from == to {
		return naiveResult{status: StatusReachable, path: []ObjectID{from}}
	}
	best := naiveResult{status: StatusUnreachable}
	ambiguous := false
	visited := map[ObjectID]bool{from: true}

	var dfs func(cur ObjectID, cost int64, path []ObjectID)
	dfs = func(cur ObjectID, cost int64, path []ObjectID) {
		if ambiguous {
			return
		}
		if cur == to {
			cand := append([]ObjectID(nil), path...)
			if best.status == StatusUnreachable ||
				cost < best.cost ||
				(cost == best.cost && compareSeq(cand, best.path) < 0) {
				best = naiveResult{status: StatusReachable, path: cand, cost: cost}
			}
			return
		}
		for _, link := range g.adj[cur] {
			d := st.naiveAuthorize(principal, link, g.types[link.From], g.types[link.To])
			if d == DecisionAmbiguous {
				ambiguous = true
				return
			}
			if d != DecisionAllow || visited[link.To] {
				continue
			}
			visited[link.To] = true
			dfs(link.To, cost+link.Cost, append(path, link.To))
			delete(visited, link.To)
			if ambiguous {
				return
			}
		}
	}
	dfs(from, 0, []ObjectID{from})

	if ambiguous {
		return naiveResult{status: StatusAmbiguous}
	}
	return best
}

// sortedGroups 返回主体所属权限组的有序列表，保证日志输出确定。
func (st *naiveState) sortedGroups(p PrincipalID) []GroupID {
	var out []GroupID
	for g := range st.members[p] {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
