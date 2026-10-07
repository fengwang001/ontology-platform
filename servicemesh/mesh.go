package servicemesh

import (
	"sync"
	"sync/atomic"
)

type serviceState struct {
	version uint64
	config  *compiledConfig
}

// Mesh 管理多服务的配置版本、子集注册与请求分流。零值即可用。
type Mesh struct {
	// mu 只保护服务表增删与版本判定；路由读路径只做原子读，永不持锁。
	mu       sync.Mutex
	services atomic.Pointer[map[string]*serviceEntry]
}

type serviceEntry struct {
	state   atomic.Pointer[serviceState]
	subsets atomic.Pointer[map[string]*subsetState]
}

type subsetState struct {
	endpoints  []Endpoint // 不可变快照，就绪端点连续排在前 readyCount 个
	readyCount int
	rrCounter  uint64 // 就绪端点轮询计数
}

// NewMesh 创建空网格。
func NewMesh() *Mesh {
	m := &Mesh{}
	empty := map[string]*serviceEntry{}
	m.services.Store(&empty)
	return m
}

// Publish 原子发布某服务配置；baseVersion 必须等于当前版本，否则版本冲突。
// 发布失败（参数非法/校验失败/版本冲突）不改变当前配置与版本。
func (m *Mesh) Publish(service string, cfg *ServiceConfig, baseVersion uint64) (uint64, *RouteError) {
	if service == "" {
		return 0, errInvalid("empty service name")
	}
	// 先在当前状态之外完成全部编译与校验。
	compiled, e := compile(cfg)
	if e != nil {
		return 0, e
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.getOrCreateEntryLocked(service)
	cur := entry.state.Load()
	if cur.version != baseVersion {
		return cur.version, errConflict(cur.version)
	}
	next := &serviceState{version: cur.version + 1, config: compiled}
	entry.state.Store(next)
	return next.version, nil
}

// RegisterSubsets 整份替换某服务的子集与端点快照。
func (m *Mesh) RegisterSubsets(service string, subsets map[string][]Endpoint) *RouteError {
	if service == "" {
		return errInvalid("empty service name")
	}
	cloned := make(map[string]*subsetState, len(subsets))
	for name, eps := range subsets {
		if name == "" {
			return errInvalid("empty subset name")
		}
		snap := make([]Endpoint, 0, len(eps))
		ready := 0
		for _, ep := range eps {
			if ep.Name == "" {
				return errInvalid("empty endpoint name in subset %q", name)
			}
			snap = append(snap, ep)
			if ep.Ready {
				ready++
			}
		}
		partitionReady(snap)
		cloned[name] = &subsetState{endpoints: snap, readyCount: ready}
	}
	m.mu.Lock()
	entry := m.getOrCreateEntryLocked(service)
	m.mu.Unlock()
	entry.subsets.Store(&cloned)
	return nil
}

// Route 按已发布配置分流一次请求。
func (m *Mesh) Route(service string, req Request) (*RouteResult, *RouteError) {
	if service == "" {
		return nil, errInvalid("empty service name")
	}
	if req.Bucket < 0 || req.Bucket > 9999 {
		return nil, errInvalid("bucket %d out of range [0,9999]", req.Bucket)
	}
	cleanPath := stripQuery(req.Path)
	if cleanPath == "" || cleanPath[0] != '/' {
		return nil, errInvalid("request path %q must start with '/'", req.Path)
	}
	entry := m.getEntry(service)
	if entry == nil {
		return nil, errNoRoute(service)
	}
	// 单次原子读取整份配置快照：请求期间不可能观察到发布进行到一半。
	snap := entry.state.Load()
	if snap == nil || snap.config == nil {
		return nil, errNoRoute(service)
	}
	cfg := snap.config

	headers := normalizeHeaders(req)

	ruleIdx := -1
	var targets []Target
	var eff Policy
	if hits := cfg.index.lookup(req.Path, headers); len(hits) > 0 {
		first := hits[0]
		ruleIdx = first.ruleIdx
		targets = cfg.rules[ruleIdx].targets
		eff = cfg.rules[ruleIdx].policies
	} else {
		if len(cfg.fallbacks) == 0 {
			return nil, errNoRoute(service)
		}
		targets = cfg.fallbacks
		eff = cfg.def
	}

	subset := pickTarget(targets, req.Bucket)
	ep, e := m.pickEndpoint(service, entry, subset)
	if e != nil {
		return nil, e
	}
	return &RouteResult{
		Service:  service,
		Subset:   subset,
		RuleIdx:  ruleIdx,
		Policy:   clonePolicy(eff),
		Endpoint: ep,
	}, nil
}

// Version 返回某服务当前配置版本（从未发布为 0）。
func (m *Mesh) Version(service string) uint64 {
	entry := m.getEntry(service)
	if entry == nil {
		return 0
	}
	return entry.state.Load().version
}

// GetConfig 返回当前配置的深拷贝与版本；调用方修改不影响内部状态。
func (m *Mesh) GetConfig(service string) (*ServiceConfig, uint64, bool) {
	entry := m.getEntry(service)
	if entry == nil {
		return nil, 0, false
	}
	snap := entry.state.Load()
	if snap.config == nil {
		return nil, snap.version, false
	}
	cfg := snap.config.toServiceConfig()
	return &cfg, snap.version, true
}

func (m *Mesh) getEntry(service string) *serviceEntry {
	return (*m.services.Load())[service]
}

// getOrCreateEntryLocked 以 copy-on-write 方式新增服务表项，读路径因此免锁。
func (m *Mesh) getOrCreateEntryLocked(service string) *serviceEntry {
	table := *m.services.Load()
	if entry := table[service]; entry != nil {
		return entry
	}
	newTable := make(map[string]*serviceEntry, len(table)+1)
	for k, v := range table {
		newTable[k] = v
	}
	entry := &serviceEntry{}
	entry.state.Store(&serviceState{version: 0})
	entry.subsets.Store(&map[string]*subsetState{})
	newTable[service] = entry
	m.services.Store(&newTable)
	return entry
}

// pickTarget 按声明顺序排布分桶：权重 w 恰好承接 w*100 个相邻分桶值。
func pickTarget(targets []Target, bucket int) string {
	start := 0
	for _, t := range targets {
		end := start + t.Weight*100
		if bucket >= start && bucket < end {
			return t.Subset
		}
		start = end
	}
	return ""
}

func (m *Mesh) pickEndpoint(service string, entry *serviceEntry, subset string) (string, *RouteError) {
	st := (*entry.subsets.Load())[subset]
	if st == nil || st.readyCount == 0 {
		// 未登记或无就绪端点：无可用端点，不改道到其他目标，也不改变任何状态。
		return "", errNoEndpoint(service, subset)
	}
	idx := atomic.AddUint64(&st.rrCounter, 1) - 1
	return st.endpoints[idx%uint64(st.readyCount)].Name, nil
}

func normalizeHeaders(req Request) map[string][]string {
	out := map[string][]string{}
	for k, v := range req.Headers {
		name := headerName(k)
		out[name] = append(out[name], v)
	}
	for k, vs := range req.HeaderValues {
		name := headerName(k)
		out[name] = append(out[name], vs...)
	}
	return out
}

// partitionReady 稳定地把就绪端点移到切片前部。
func partitionReady(eps []Endpoint) {
	ready := make([]Endpoint, 0, len(eps))
	notReady := make([]Endpoint, 0, len(eps))
	for _, ep := range eps {
		if ep.Ready {
			ready = append(ready, ep)
		} else {
			notReady = append(notReady, ep)
		}
	}
	copy(eps, append(ready, notReady...))
}
