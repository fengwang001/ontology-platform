package endpointshard

import (
	"math/rand"
	"sync"
)

// Manager 管理一组服务的分片维护器。
//
// 错误优先级（高 -> 低）：参数非法 > 服务不存在 > 服务已存在。
// 每个操作都按此顺序检查，因此被同时命中多个条件时只报最高优先级者；
// 被拒绝的操作不改变任何状态（包括分片编号与代次）。
//
// 并发：服务表由互斥锁保护；每个服务内部有独立的读写锁，
// 同一服务上的同步/调整与读取互不交错，任意并发调用的结果
// 等价于某个串行顺序。
type Manager struct {
	mu       sync.Mutex
	services map[string]*Service
}

// NewManager 创建一个空的管理器。
func NewManager() *Manager {
	return &Manager{services: make(map[string]*Service)}
}

// CreateService 创建服务；M 必须为正整数。
func (m *Manager) CreateService(name string, maxPerShard int) error {
	const op = "create"
	if name == "" {
		return invalidArg(op, name, "service name must not be empty")
	}
	if maxPerShard <= 0 {
		return invalidArg(op, name, "shard capacity must be a positive integer")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.services[name]; ok {
		return alreadyExists(op, name)
	}
	// 固定种子：索引内部形状可复现；可观察输出与随机源无关。
	m.services[name] = newService(maxPerShard, rand.New(rand.NewSource(1619)))
	return nil
}

// DeleteService 删除服务及其全部分片。
func (m *Manager) DeleteService(name string) error {
	const op = "delete"
	if name == "" {
		return invalidArg(op, name, "service name must not be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.services[name]; !ok {
		return notFound(op, name)
	}
	delete(m.services, name)
	return nil
}

// Sync 把服务的分片调整到与期望端点全集一致，返回变更报告。
func (m *Manager) Sync(name string, desired []Endpoint) (ChangeReport, error) {
	const op = "sync"
	if name == "" {
		return ChangeReport{}, invalidArg(op, name, "service name must not be empty")
	}
	if err := validateDesired(op, name, desired); err != nil {
		return ChangeReport{}, err
	}
	svc, err := m.get(op, name)
	if err != nil {
		return ChangeReport{}, err
	}
	return svc.Sync(desired), nil
}

// Resize 修改服务的分片容量 M，返回变更报告。
func (m *Manager) Resize(name string, maxPerShard int) (ChangeReport, error) {
	const op = "resize"
	if name == "" {
		return ChangeReport{}, invalidArg(op, name, "service name must not be empty")
	}
	if maxPerShard <= 0 {
		return ChangeReport{}, invalidArg(op, name, "shard capacity must be a positive integer")
	}
	svc, err := m.get(op, name)
	if err != nil {
		return ChangeReport{}, err
	}
	return svc.Resize(maxPerShard), nil
}

// Query 消费者按所在区域查询。
func (m *Manager) Query(name, region string) (QueryResult, error) {
	const op = "query"
	if name == "" {
		return QueryResult{}, invalidArg(op, name, "service name must not be empty")
	}
	svc, err := m.get(op, name)
	if err != nil {
		return QueryResult{}, err
	}
	return svc.Query(region), nil
}

// Describe 返回服务的只读快照（用于观察者自检与测试对照）。
func (m *Manager) Describe(name string) (ServiceView, error) {
	const op = "describe"
	if name == "" {
		return ServiceView{}, invalidArg(op, name, "service name must not be empty")
	}
	svc, err := m.get(op, name)
	if err != nil {
		return ServiceView{}, err
	}
	return svc.Describe(), nil
}

// ResetStats 清零服务的操作计数器。
func (m *Manager) ResetStats(name string) error {
	const op = "stats/reset"
	svc, err := m.get(op, name)
	if err != nil {
		return err
	}
	svc.ResetStats()
	return nil
}

// Stats 返回服务的操作计数器快照。
func (m *Manager) Stats(name string) (StatsSnapshot, error) {
	const op = "stats"
	svc, err := m.get(op, name)
	if err != nil {
		return StatsSnapshot{}, err
	}
	return svc.Stats(), nil
}

func (m *Manager) get(op, name string) (*Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	svc, ok := m.services[name]
	if !ok {
		return nil, notFound(op, name)
	}
	return svc, nil
}

// validateDesired 校验期望端点全集：标识非空且不重复、区域非空。
func validateDesired(op, service string, desired []Endpoint) error {
	seen := make(map[string]struct{}, len(desired))
	for _, ep := range desired {
		if ep.ID == "" {
			return invalidArg(op, service, "endpoint id must not be empty")
		}
		if ep.Region == "" {
			return invalidArg(op, service, "endpoint region must not be empty")
		}
		if _, dup := seen[ep.ID]; dup {
			return invalidArg(op, service, "duplicate endpoint id: "+ep.ID)
		}
		seen[ep.ID] = struct{}{}
	}
	return nil
}
