package ontology

// NaiveResult 是朴素穷举参照模型的结果。
//
// 该模型用与主遍历器完全不同的写法（递归 + 全局 visited 集合 +
// 先求后继再切片）独立刻画同一语义，作为随机差分测试的“另一个真相源”。
type NaiveResult struct {
	Order    []Object
	Trunc    Truncation
	Excluded map[string]struct{}
}

type naiveEnv struct {
	snap     *snapshotHandle
	perms    PermissionModel
	actor    Actor
	depthCap int
	fanCap   int
	visited  map[string]bool
	excluded map[string]struct{}
	order    []Object
	fanout   bool
	depth    bool
}

func (e *naiveEnv) successors(u string) []string {
	bag := map[string]bool{}
	for _, key := range e.snap.outgoing(u) {
		link := Link{Type: key.typ, Source: key.source, Target: key.target}
		if _, ok := e.snap.linkType(key.typ); !ok {
			continue
		}
		if !e.perms.CanTraverse(e.snap, e.actor, link) {
			continue
		}
		if !e.perms.CanSee(e.snap, e.actor, key.target) {
			continue
		}
		if _, ok := e.snap.object(key.target); !ok {
			continue
		}
		bag[key.target] = true
	}
	var out []string
	for v := range bag {
		if !e.visited[v] && !e.isExcluded(v) {
			out = append(out, v)
		}
	}
	sortStrings(out)
	return out
}

func (e *naiveEnv) isExcluded(id string) bool {
	_, ok := e.excluded[id]
	return ok
}

func (e *naiveEnv) walk(u string, level int) {
	obj, ok := e.snap.object(u)
	if !ok {
		return
	}
	e.visited[u] = true
	e.order = append(e.order, *obj)

	succ := e.successors(u)

	if level == e.depthCap {
		if len(succ) > 0 {
			e.depth = true
		}
		return
	}

	if len(succ) > e.fanCap {
		e.fanout = true
		for _, v := range succ[e.fanCap:] {
			e.excluded[v] = struct{}{}
		}
		succ = succ[:e.fanCap]
	}
	for _, v := range succ {
		if e.visited[v] || e.isExcluded(v) {
			continue
		}
		e.walk(v, level+1)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// NaiveTraverse 在给定快照上一次性穷举完整邻域。
func NaiveTraverse(snap *snapshotHandle, perms PermissionModel, actor Actor, start string, maxDepth, maxFanout int) (*NaiveResult, error) {
	if maxDepth < 0 || maxFanout < 0 {
		return nil, ErrInvalidArgument
	}
	env := &naiveEnv{
		snap:     snap.forkMetrics(),
		perms:    perms,
		actor:    actor,
		depthCap: maxDepth,
		fanCap:   maxFanout,
		visited:  map[string]bool{},
		excluded: map[string]struct{}{},
	}
	if _, ok := env.snap.object(start); !ok || !perms.CanSee(env.snap, actor, start) {
		return nil, ErrForbidden
	}
	env.walk(start, 0)

	trunc := TruncComplete
	if env.depth {
		trunc = TruncDepth
	}
	if env.fanout {
		trunc = TruncFanout
	}
	return &NaiveResult{Order: env.order, Trunc: trunc, Excluded: env.excluded}, nil
}
