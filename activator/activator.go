// Package activator 编排路由待激活版本、依赖集群预热与级联切换。
package activator

import (
	"sort"

	"ontology/config"
	"ontology/warming"
	"sync"
)

type routeState struct {
	servingVer int64
	pendingVer int64
	servingRef []string
	pendingRef []string
}

func cloneRefs(refs []string) []string {
	if refs == nil {
		return nil
	}
	cp := make([]string, len(refs))
	copy(cp, refs)
	return cp
}

func cloneRoutes(rt map[string]*routeState) map[string]*routeState {
	cp := make(map[string]*routeState, len(rt))
	for name, r := range rt {
		cp[name] = &routeState{
			servingVer: r.servingVer,
			pendingVer: r.pendingVer,
			servingRef: cloneRefs(r.servingRef),
			pendingRef: cloneRefs(r.pendingRef),
		}
	}
	return cp
}

// Activator 是依赖感知的网关配置预热激活器；并发安全。
type Activator struct {
	mu       sync.Mutex
	w        int64
	lastVer  int64
	maxNow   int64
	clusters *warming.Table
	routes   map[string]*routeState

	// lastReadyChecks 为非导出计数器：最近一次 Ready 实际检查的待激活路由数。
	// 规格上界为“引用该集群的待激活路由数 + 1”（+1 为该集群自身的晋升尝试）。
	lastReadyChecks int
}

// New 创建预热期限为 W（毫秒，[1,1e9]）的激活器；W 非法返回 nil。
func New(w int64) *Activator {
	if !config.ValidWarmup(w) {
		return nil
	}
	return &Activator{
		w:        w,
		clusters: warming.NewTable(w),
		routes:   map[string]*routeState{},
	}
}

// draft 是锁内可变草稿；校验失败直接丢弃即可保证 NACK 不落盘。
type draft struct {
	clusters *warming.Table
	routes   map[string]*routeState
}

// settle 按 (since,name) 升序结算超时：新集群失败则移除并连带丢弃待激活路由；
// 更新失败保留旧在役版本，并让引用它的待激活路由按普通条件重新检查（可能激活）。
func (d *draft) settle(now int64) {
	for _, name := range d.clusters.ExpireDue(now) {
		if d.clusters.DropWarming(name) {
			d.cascade()
		} else {
			d.dropPendingReferencing(name)
		}
	}
}

// dropPendingReferencing 丢弃所有待激活版本引用了集群 name 的路由的待激活版本。
func (d *draft) dropPendingReferencing(name string) {
	for rn, r := range d.routes {
		if r.pendingVer == 0 {
			continue
		}
		for _, ref := range r.pendingRef {
			if ref == name {
				r.pendingVer, r.pendingRef = 0, nil
				if r.servingVer == 0 {
					delete(d.routes, rn)
				}
				break
			}
		}
	}
}

// refsReady 判断一组引用集群是否全部就绪。
func (d *draft) refsReady(refs []string) bool {
	for _, ref := range refs {
		if !d.clusters.IsReady(ref) {
			return false
		}
	}
	return true
}

// cascade 是“所有引用集群就绪则待激活版本晋升在役”的级联激活。
// 扫描按路由名字典序，直到一趟没有晋升为止；它与超时结算同处一个临界区。
func (d *draft) cascade() {
	for {
		names := make([]string, 0, len(d.routes))
		for name, r := range d.routes {
			if r.pendingVer != 0 {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		promoted := false
		for _, name := range names {
			r := d.routes[name]
			if r.pendingVer != 0 && d.refsReady(r.pendingRef) {
				r.servingVer = r.pendingVer
				r.servingRef = cloneRefs(r.pendingRef)
				r.pendingVer, r.pendingRef = 0, nil
				promoted = true
			}
		}
		if !promoted {
			return
		}
	}
}

// readyCascade 只检查待激活引用中包含 cluster 的路由，返回检查条数。
func (d *draft) readyCascade(cluster string) int {
	names := make([]string, 0)
	for name, r := range d.routes {
		if r.pendingVer == 0 {
			continue
		}
		for _, ref := range r.pendingRef {
			if ref == cluster {
				names = append(names, name)
				break
			}
		}
	}
	sort.Strings(names)
	for _, name := range names {
		r := d.routes[name]
		if r.pendingVer != 0 && d.refsReady(r.pendingRef) {
			r.servingVer = r.pendingVer
			r.servingRef = cloneRefs(r.pendingRef)
			r.pendingVer, r.pendingRef = 0, nil
		}
	}
	return len(names)
}

func (a *Activator) snapshot() *draft {
	return &draft{clusters: a.clusters.Clone(), routes: cloneRoutes(a.routes)}
}

func (a *Activator) commit(d *draft, now int64) {
	a.clusters, a.routes = d.clusters, d.routes
	a.maxNow = now
}

func checkTime(now int64) error {
	if !config.ValidTime(now) {
		return config.ErrInvalidTime
	}
	return nil
}

// Push 接收序号为 ver 的推送；接受时推进在役/预热/待激活状态并级联激活。
func (a *Activator) Push(ver int64, in config.PushInput, now int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 校验次序：参数非法 > 时间非法 > 时钟回退 > 序号不符 > 悬空 > 在用。
	if err := config.ValidateParams(in); err != nil {
		return err
	}
	if err := checkTime(now); err != nil {
		return err
	}
	if now < a.maxNow {
		return config.ErrClockBackwards
	}

	d := a.snapshot()
	d.settle(now) // 超时是时间的纯函数；NACK 后草稿丢弃，不落盘。

	if ver != a.lastVer+1 {
		return config.ErrBadVersion
	}

	// 推送后的集群集合：结算后仍存在且未被本次删除者 + 本次新增/更新者。
	postClusters := make(map[string]bool)
	for name := range d.clusters.Clusters {
		postClusters[name] = true
	}
	for _, name := range in.DeleteClusters {
		delete(postClusters, name)
	}
	for _, c := range in.Clusters {
		postClusters[c.Name] = true
	}
	if err := config.CheckDangling(in.Routes, postClusters); err != nil {
		return err
	}

	// 在“删路由 → 集群增改（尚未应用，仅影响在役视图：不存在变更在役）
	// → 路由增改待激活视图按新引用”后的路由视图上做“在用”检查。
	postRoutes := make(map[string]config.RouteView, len(d.routes))
	for rn, r := range d.routes {
		postRoutes[rn] = config.RouteView{ServingRefs: r.servingRef, PendingRefs: r.pendingRef}
	}
	for _, rn := range in.DeleteRoutes {
		delete(postRoutes, rn) // 删除路由不计
	}
	for _, nr := range in.Routes {
		v := postRoutes[nr.Name]
		v.PendingRefs = nr.Clusters // 更新路由的待激活版本以新引用计
		postRoutes[nr.Name] = v
	}
	if err := config.CheckInUse(in.DeleteClusters, postRoutes); err != nil {
		return err
	}

	// 依次应用：先删路由；集群增改进入预热；删集群；路由增改登记待激活。
	for _, rn := range in.DeleteRoutes {
		delete(d.routes, rn)
	}
	for _, c := range in.Clusters {
		d.clusters.PutWarming(c.Name, ver, now)
	}
	for _, name := range in.DeleteClusters {
		d.clusters.Delete(name)
	}
	for _, nr := range in.Routes {
		r := d.routes[nr.Name]
		if r == nil {
			r = &routeState{}
			d.routes[nr.Name] = r
		}
		r.pendingVer = ver
		r.pendingRef = cloneRefs(nr.Clusters)
	}

	a.lastVer = ver
	d.cascade()
	a.commit(d, now)
	return nil
}

// Ready 报告集群预热完成，并只检查引用它的待激活路由。
func (a *Activator) Ready(name string, now int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if name == "" {
		return config.ErrInvalidArgument
	}
	if err := checkTime(now); err != nil {
		return err
	}
	if now < a.maxNow {
		return config.ErrClockBackwards
	}

	d := a.snapshot()
	d.settle(now)

	s, ok := d.clusters.Get(name)
	if !ok || s.WarmingVer == 0 {
		return config.ErrNotWarming
	}

	d.clusters.Ready(name)
	a.lastReadyChecks = 1 + d.readyCascade(name) // +1：集群自身晋升这一次检查
	a.commit(d, now)
	return nil
}

// Serving 查询路由当前在役版本；无在役版本时返回 (0,false)。
func (a *Activator) Serving(route string, now int64) (int64, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := checkTime(now); err != nil {
		return 0, false, err
	}
	if now < a.maxNow {
		return 0, false, config.ErrClockBackwards
	}

	d := a.snapshot()
	d.settle(now)
	r := d.routes[route]
	a.commit(d, now)
	if r == nil || r.servingVer == 0 {
		return 0, false, nil
	}
	return r.servingVer, true, nil
}

// State 查询集群状态（在役版本、预热版本、since）；集群不存在时 ok=false。
func (a *Activator) State(cluster string, now int64) (warming.ClusterState, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := checkTime(now); err != nil {
		return warming.ClusterState{}, false, err
	}
	if now < a.maxNow {
		return warming.ClusterState{}, false, config.ErrClockBackwards
	}

	d := a.snapshot()
	d.settle(now)
	st, ok := d.clusters.Get(cluster)
	a.commit(d, now)
	return st, ok, nil
}

// lastReadyCascadeBound 供同包白盒测试读取最近一次 Ready 的级联检查计数。
func (a *Activator) lastReadyCascadeBound() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastReadyChecks
}
