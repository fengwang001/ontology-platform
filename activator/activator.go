// Package activator 协调集群预热、路由待激活与依赖感知的级联切换。
package activator

import (
	"sort"
	"sync"

	"ontology/config"
	"ontology/warming"
)

// Status 是一次查询或操作后单个资源的版本状态。
type Status struct {
	Version        int64
	WarmingVersion int64
	Since          int64
	Present        bool
	HasWarming     bool
}

// Activator 是预热激活器；全部方法可并发调用，等价于某一串行顺序。
type Activator struct {
	mu           sync.Mutex
	W            int64
	maxNow       int64
	lastAccepted int64

	clusters map[string]*warming.Cluster

	routes map[string]*routeState
	// pendRefCount[cluster] = 引用该集群的待激活路由数（非导出计数器）。
	pendRefCount map[string]int

	// 最近一次 Ready 级联实际检查过的路由数（非导出，用于复杂度证明）。
	lastCascadeChecks int
}

type routeState struct {
	servingVersion int64
	hasServing     bool
	servingRefs    []string

	pendingVersion int64
	hasPending     bool
	pendingRefs    []string
}

// New 以预热期限 W（1..1e9 毫秒）构造激活器。
func New(W int64) *Activator {
	if W < 1 || W > 1_000_000_000 {
		panic("activator: W out of range [1,1e9]")
	}
	return &Activator{
		mu:           sync.Mutex{},
		W:            W,
		clusters:     map[string]*warming.Cluster{},
		routes:       map[string]*routeState{},
		pendRefCount: map[string]int{},
	}
}

// Push 校验并按序号应用一次配置推送。
func (a *Activator) Push(ver int64, up config.Push, now int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := config.CheckParams(up); err != nil {
		return err
	}
	if err := config.CheckTime(now, a.maxNow); err != nil {
		return err
	}
	if ver != a.lastAccepted+1 {
		return config.ErrBadVersion
	}

	w := a.snapshot()
	a.settle(w, now)

	clusterDel := stringSet(up.ClusterDeletes)
	routeDel := stringSet(up.RouteDeletes)

	// 推送后的集群集合：现存未删除者 ∪ 新增/更新者。
	postClusters := make(map[string]bool, len(w.clusters)+len(up.ClusterUpserts))
	for name := range w.clusters {
		if !clusterDel[name] {
			postClusters[name] = true
		}
	}
	for _, name := range up.ClusterUpserts {
		postClusters[name] = true
	}
	if err := config.CheckDangling(up.RouteUpserts, postClusters); err != nil {
		return err
	}

	existing := make(map[string]config.ExistingRoute, len(w.routes))
	for name, rt := range w.routes {
		info := config.ExistingRoute{
			Deleted:     routeDel[name],
			Updated:     false,
			ServingRefs: a.servingRefs(w, rt),
			HasPending:  rt.hasPending,
			PendingRefs: rt.pendingRefs,
		}
		existing[name] = info
	}
	for _, ru := range up.RouteUpserts {
		info := existing[ru.Name]
		info.Updated = true
		info.Deleted = false
		info.HasPending = true
		info.PendingRefs = append([]string(nil), ru.Refs...)
		existing[ru.Name] = info
	}
	if err := config.CheckInUse(up.ClusterDeletes, existing); err != nil {
		return err
	}

	// 1) 删除路由（在役与待激活一并移除）。
	for _, name := range up.RouteDeletes {
		if rt, ok := w.routes[name]; ok {
			if rt.hasPending {
				for _, c := range rt.pendingRefs {
					w.pendRefCount[c]--
					if w.pendRefCount[c] == 0 {
						delete(w.pendRefCount, c)
					}
				}
			}
			delete(w.routes, name)
		}
	}

	// 2) 集群新增/更新进入预热（取代旧预热并重置计时）。
	for _, name := range up.ClusterUpserts {
		c := w.clusters[name]
		if c == nil {
			c = &warming.Cluster{}
			w.clusters[name] = c
		}
		c.StartWarming(ver, now)
	}

	// 3) 删除集群（连同预热版本）立即移除；不存在视为无操作。
	for _, name := range up.ClusterDeletes {
		delete(w.clusters, name)
	}

	// 4) 路由新增/更新登记为待激活版本（取代旧待激活，在役继续服务）。
	for _, ru := range up.RouteUpserts {
		rt := w.routes[ru.Name]
		if rt == nil {
			rt = &routeState{}
			w.routes[ru.Name] = rt
		}
		if rt.hasPending {
			for _, c := range rt.pendingRefs {
				w.pendRefCount[c]--
				if w.pendRefCount[c] == 0 {
					delete(w.pendRefCount, c)
				}
			}
		}
		rt.pendingVersion = ver
		rt.hasPending = true
		rt.pendingRefs = append([]string(nil), ru.Refs...)
		for _, c := range ru.Refs {
			w.pendRefCount[c]++
		}
	}

	w.lastAccepted = ver
	// 5) 级联激活。
	a.activateAll(w)
	a.commit(w, now)
	return nil
}

// Ready 报告集群预热完成并触发受限的级联激活。
func (a *Activator) Ready(name string, now int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if name == "" {
		return config.ErrInvalidParam
	}
	if err := config.CheckTime(now, a.maxNow); err != nil {
		return err
	}
	w := a.snapshot()
	a.settle(w, now)

	c := w.clusters[name]
	if c == nil || !c.HasWarming {
		return config.ErrNotWarming
	}
	c.Promote()

	// 只检查引用该集群的待激活路由；检查次数不超过其数量 +1。
	checks := 0
	candidates := a.routesReferencing(w, name)
	for _, rn := range candidates {
		rt := w.routes[rn]
		if rt == nil || !rt.hasPending {
			continue
		}
		checks++
		if a.refsReady(w, rt.pendingRefs) {
			a.activateRoute(w, rn, rt)
		}
	}
	a.lastCascadeChecks = checks

	a.commit(w, now)
	return nil
}

// Serving 返回路由当前在役版本；不存在时第二个返回值为 false。
func (a *Activator) Serving(route string, now int64) (int64, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := config.CheckTime(now, a.maxNow); err != nil {
		return 0, false, err
	}
	w := a.snapshot()
	a.settle(w, now)
	rt := w.routes[route]
	if rt == nil || !rt.hasServing {
		a.commit(w, now)
		return 0, false, nil
	}
	a.commit(w, now)
	return rt.servingVersion, true, nil
}

// State 返回集群版本状态；不存在时 Present 为 false。
func (a *Activator) State(cluster string, now int64) (Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := config.CheckTime(now, a.maxNow); err != nil {
		return Status{}, err
	}
	w := a.snapshot()
	a.settle(w, now)
	c := w.clusters[cluster]
	if c == nil {
		a.commit(w, now)
		return Status{Present: false}, nil
	}
	a.commit(w, now)
	return Status{
		Version:        c.ServingVersion,
		WarmingVersion: c.WarmingVersion,
		Since:          c.Since,
		Present:        true,
		HasWarming:     c.HasWarming,
	}, nil
}

// ---- 内部状态与原子提交 ----

type world struct {
	clusters     map[string]*warming.Cluster
	routes       map[string]*routeState
	pendRefCount map[string]int
	lastAccepted int64
}

func (a *Activator) snapshot() *world {
	w := &world{
		clusters:     make(map[string]*warming.Cluster, len(a.clusters)),
		routes:       make(map[string]*routeState, len(a.routes)),
		pendRefCount: make(map[string]int, len(a.pendRefCount)),
		lastAccepted: a.lastAccepted,
	}
	for name, c := range a.clusters {
		cp := *c
		w.clusters[name] = &cp
	}
	for name, rt := range a.routes {
		cp := &routeState{
			servingVersion: rt.servingVersion,
			hasServing:     rt.hasServing,
			servingRefs:    append([]string(nil), rt.servingRefs...),
			pendingVersion: rt.pendingVersion,
			hasPending:     rt.hasPending,
			pendingRefs:    append([]string(nil), rt.pendingRefs...),
		}
		w.routes[name] = cp
	}
	for c, n := range a.pendRefCount {
		w.pendRefCount[c] = n
	}
	return w
}

func (a *Activator) commit(w *world, now int64) {
	a.clusters = w.clusters
	a.routes = w.routes
	a.pendRefCount = w.pendRefCount
	a.lastAccepted = w.lastAccepted
	a.maxNow = now
}

// ---- 超时结算（now 的纯函数，在克隆上执行）----

func (a *Activator) settle(w *world, now int64) {
	type timed struct {
		name  string
		since int64
	}
	var ts []timed
	for name, c := range w.clusters {
		if c.TimedOut(a.W, now) {
			ts = append(ts, timed{name, c.Since})
		}
	}
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].since != ts[j].since {
			return ts[i].since < ts[j].since
		}
		return ts[i].name < ts[j].name
	})
	for _, t := range ts {
		c := w.clusters[t.name]
		if c == nil || !c.HasWarming || !c.TimedOut(a.W, now) {
			continue
		}
		hasServing := c.FailWarming()
		if !hasServing {
			// 无在役版本：移除集群，并丢弃所有引用它的待激活路由版本。
			delete(w.clusters, t.name)
			for rn, rt := range w.routes {
				if rt.hasPending && containsStr(rt.pendingRefs, t.name) {
					a.dropPending(w, rn, rt)
				}
			}
		}
	}
	// 保留旧在役版本的集群可能让待激活路由变为满足条件，按普通条件重检。
	a.activateAll(w)
}

// ---- 级联激活 ----

func (a *Activator) activateAll(w *world) {
	names := make([]string, 0, len(w.routes))
	for name, rt := range w.routes {
		if rt.hasPending {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	activated := true
	for activated {
		activated = false
		for _, rn := range names {
			rt := w.routes[rn]
			if rt == nil || !rt.hasPending {
				continue
			}
			if a.refsReady(w, rt.pendingRefs) {
				a.activateRoute(w, rn, rt)
				activated = true
			}
		}
	}
}

func (a *Activator) activateRoute(w *world, name string, rt *routeState) {
	rt.servingVersion = rt.pendingVersion
	rt.hasServing = true
	rt.servingRefs = append([]string(nil), rt.pendingRefs...)
	for _, c := range rt.pendingRefs {
		w.pendRefCount[c]--
		if w.pendRefCount[c] == 0 {
			delete(w.pendRefCount, c)
		}
	}
	rt.hasPending = false
	rt.pendingVersion = 0
	rt.pendingRefs = nil
}

func (a *Activator) dropPending(w *world, name string, rt *routeState) {
	for _, c := range rt.pendingRefs {
		w.pendRefCount[c]--
		if w.pendRefCount[c] == 0 {
			delete(w.pendRefCount, c)
		}
	}
	rt.hasPending = false
	rt.pendingVersion = 0
	rt.pendingRefs = nil
}

func (a *Activator) refsReady(w *world, refs []string) bool {
	for _, c := range refs {
		cl := w.clusters[c]
		if cl == nil || !cl.Ready() {
			return false
		}
	}
	return true
}

func (a *Activator) routesReferencing(w *world, cluster string) []string {
	var out []string
	for name, rt := range w.routes {
		if rt.hasPending && containsStr(rt.pendingRefs, cluster) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// servingRefs 重建一条在役路由引用的集群列表：在役引用只随推送更新替换，
// 故保存于 routeState.servingRefs（见 activateRoute 的赋值侧）。
func (a *Activator) servingRefs(w *world, rt *routeState) []string {
	_ = w
	return rt.servingRefs
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func stringSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
