package ontology

import (
	"slices"
	"sort"
)

// Status 是路径查询的结果状态。
type Status int

const (
	// StatusOK 表示查询有唯一确定的最小代价路径。
	StatusOK Status = iota
	// StatusInvalidArgument 表示参数非法（角色序列为空或角色未声明）。
	StatusInvalidArgument
	// StatusPermissionDenied 表示起点或终点对调用者无存在性权限。
	StatusPermissionDenied
	// StatusUnreachable 表示不存在任何满足角色序列的候选路径。
	StatusUnreachable
	// StatusAmbiguous 表示存在候选路径，但因歧义步无法确定唯一结果。
	StatusAmbiguous
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusInvalidArgument:
		return "InvalidArgument"
	case StatusPermissionDenied:
		return "PermissionDenied"
	case StatusUnreachable:
		return "Unreachable"
	case StatusAmbiguous:
		return "Ambiguous"
	}
	return "Unknown"
}

// Query 是一次角色化路径查询的输入。
type Query struct {
	Caller Caller
	Start  ObjectID
	End    ObjectID
	Roles  []Role
}

// Step 是结果路径上的一步。
type Step struct {
	Index    int
	From     ObjectID
	To       ObjectID
	LinkType LinkTypeName
	Role     Role
	Cost     int64
}

// AmbiguousStep 定位一次查询中遇到的歧义步。
type AmbiguousStep struct {
	Index int
	At    ObjectID
	Role  Role
	// Candidates 为该步在最小优先级上并列的候选链接类型（按名称排序）。
	Candidates []LinkTypeName
	Priority   int
}

// Result 是路径查询的输出。
type Result struct {
	Status Status
	// Steps 仅在 Status 为 StatusOK 时非空。
	Steps     []Step
	TotalCost int64
	// Ambiguous 记录搜索过程中遇到的全部歧义步（按步序号与对象排序）。
	Ambiguous []AmbiguousStep
}

// QueryMetrics 是内部可验证、不对调用者暴露的开销度量。
type QueryMetrics struct {
	// ExpandedLinks 为本次查询实际展开的候选链接实例数目。
	// 其增长只与路径长度及各步候选链接类型数目相关，与图总体规模无关。
	ExpandedLinks int64
	// StatesExplored 为本次查询实际展开的状态（对象, 步序号）数目。
	StatesExplored int64
}

// Query 执行角色化路径查询。
func (s *Store) Query(q Query) Result {
	res, _ := s.queryWithMetrics(q)
	return res
}

// queryWithMetrics 执行查询并返回内部度量，仅供实现内部与测试使用。
func (s *Store) queryWithMetrics(q Query) (Result, QueryMetrics) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 判定次序：参数非法 > 存在性权限 > 不可达 > 歧义 > 唯一路径。
	if len(q.Roles) == 0 {
		return Result{Status: StatusInvalidArgument}, QueryMetrics{}
	}
	for _, r := range q.Roles {
		if s.roleRefs[r] <= 0 {
			return Result{Status: StatusInvalidArgument}, QueryMetrics{}
		}
	}
	if !s.hasExistenceLocked(q.Caller, q.Start) || !s.hasExistenceLocked(q.Caller, q.End) {
		return Result{Status: StatusPermissionDenied}, QueryMetrics{}
	}

	sv := &solver{
		store:  s,
		roles:  q.Roles,
		end:    q.End,
		caller: q.Caller,
		memo:   make(map[qstate]qnode),
	}
	root := sv.solve(qstate{obj: q.Start, i: 0})

	res := Result{Ambiguous: sv.ambiguous}
	if root.clean != nil {
		res.Status = StatusOK
		res.Steps = root.clean.steps
		res.TotalCost = root.clean.cost
		return res, sv.metrics
	}
	if len(sv.ambiguous) > 0 {
		res.Status = StatusAmbiguous
		return res, sv.metrics
	}
	res.Status = StatusUnreachable
	return res, sv.metrics
}

func (s *Store) hasExistenceLocked(c Caller, obj ObjectID) bool {
	_, ok := s.existence[c][obj]
	return ok
}

func (s *Store) hasTraversalLocked(c Caller, typ LinkTypeName) bool {
	_, ok := s.traversal[c][typ]
	return ok
}

// qstate 是搜索状态：处于对象 obj、即将迈出第 i 步。
type qstate struct {
	obj ObjectID
	i   int
}

// qnode 是一个状态的求解结果。
type qnode struct {
	// viable 表示从该状态出发是否存在满足剩余角色序列的合法路径
	// （不考虑歧义消解，仅存在性）。
	viable bool
	// clean 为从该状态出发、每一步都被唯一消解的最优路径后缀；
	// 不存在时为 nil。
	clean *pathTail
}

// pathTail 是从某状态到终点的路径后缀。
type pathTail struct {
	cost  int64
	objs  []ObjectID // 途经对象标识序列（含当前对象与终点）
	steps []Step
}

type solver struct {
	store  *Store
	roles  []Role
	end    ObjectID
	caller Caller

	memo      map[qstate]qnode
	ambiguous []AmbiguousStep
	metrics   QueryMetrics
}

func (sv *solver) solve(st qstate) qnode {
	if st.i == len(sv.roles) {
		if st.obj == sv.end {
			return qnode{viable: true, clean: &pathTail{objs: []ObjectID{st.obj}}}
		}
		return qnode{}
	}
	if n, ok := sv.memo[st]; ok {
		return n
	}
	sv.metrics.StatesExplored++

	role := sv.roles[st.i]
	s := sv.store

	type cand struct {
		typ         LinkTypeName
		priority    int
		cost        int64
		anyViable   bool
		bestViaType *pathTail // 经该类型某条实例的最优干净后缀
	}

	var cands []*cand
	for typ, tos := range s.adj[st.obj][role] {
		if !s.hasTraversalLocked(sv.caller, typ) {
			continue // 无遍历权限的链接类型在该步候选集合中排除
		}
		lt := s.linkTypes[typ]
		c := &cand{typ: typ, priority: lt.Roles[role], cost: lt.Cost}
		for _, to := range tos {
			sv.metrics.ExpandedLinks++
			child := sv.solve(qstate{obj: to, i: st.i + 1})
			if child.viable {
				c.anyViable = true
			}
			if child.clean != nil {
				tail := &pathTail{
					cost:  lt.Cost + child.clean.cost,
					objs:  append([]ObjectID{st.obj}, child.clean.objs...),
					steps: append([]Step{{Index: st.i, From: st.obj, To: to, LinkType: typ, Role: role, Cost: lt.Cost}}, child.clean.steps...),
				}
				if c.bestViaType == nil || lessTail(tail, c.bestViaType) {
					c.bestViaType = tail
				}
			}
		}
		cands = append(cands, c)
	}

	var res qnode
	// 只有「连接的对象组合都能继续构成合法路径」的候选才参与优先级消歧。
	var viable []*cand
	for _, c := range cands {
		if c.anyViable {
			viable = append(viable, c)
		}
	}
	if len(viable) == 0 {
		sv.memo[st] = res
		return res
	}
	res.viable = true

	minPri := viable[0].priority
	for _, c := range viable[1:] {
		if c.priority < minPri {
			minPri = c.priority
		}
	}
	var top []*cand
	for _, c := range viable {
		if c.priority == minPri {
			top = append(top, c)
		}
	}

	if len(top) > 1 {
		// 优先级相同：该步无法消解，标记为歧义步，不展开任何分支。
		names := make([]LinkTypeName, 0, len(top))
		for _, c := range top {
			names = append(names, c.typ)
		}
		sort.Slice(names, func(a, b int) bool { return names[a] < names[b] })
		sv.ambiguous = append(sv.ambiguous, AmbiguousStep{
			Index: st.i, At: st.obj, Role: role, Candidates: names, Priority: minPri,
		})
		sv.memo[st] = res
		return res
	}

	res.clean = top[0].bestViaType
	sv.memo[st] = res
	return res
}

// lessTail 比较两条路径后缀：先总代价，再对象标识序列字典序。
func lessTail(a, b *pathTail) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	return slices.Compare(a.objs, b.objs) < 0
}
