package federation

import (
	"math/big"
	"sort"
	"sync"
)

// snapshotCluster 是某次分配所见的一致快照条目。
type snapshotCluster struct {
	cluster *Cluster
	dead    bool // 已删除集群：目标恒为零，当前副本必须迁出
}

// Registry 支持并发动态增删改的集群登记表。
// 所有方法互斥保证线性一致性；分配在 RLock 保护下取出不可变深拷贝快照，
// 随后在锁外执行纯函数计算，因此一次分配所见的必为某一时刻的一致集合。
type Registry struct {
	mu        sync.RWMutex
	live      map[string]*Cluster
	tombstone map[string]*Cluster
}

func NewRegistry() *Registry {
	return &Registry{
		live:      make(map[string]*Cluster),
		tombstone: make(map[string]*Cluster),
	}
}

// Register 登记新集群。同名集群已存在（含存活登记）时拒绝。
// 若该名称此前被删除且尚未重新登记，则允许重新登记，并清除其迁出墓碑。
func (r *Registry) Register(c *Cluster) error {
	if err := validateCluster(c); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.live[c.Name]; ok {
		return invalidParam("cluster %q is already registered", c.Name)
	}
	r.live[c.Name] = cloneCluster(c)
	delete(r.tombstone, c.Name)
	return nil
}

// Update 覆盖式修改已登记集群；目标不存在时拒绝。
func (r *Registry) Update(c *Cluster) error {
	if err := validateCluster(c); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.live[c.Name]; !ok {
		return invalidParam("cluster %q is not registered", c.Name)
	}
	r.live[c.Name] = cloneCluster(c)
	return nil
}

// Remove 删除集群。删除后其已承载副本在下次分配的变更计划中视为必须迁出，
// 因此墓碑保留最后一次配置（尤其 Current）直至同名集群重新登记。
func (r *Registry) Remove(name string) error {
	if name == "" {
		return invalidParam("cluster name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.live[name]
	if !ok {
		return invalidParam("cluster %q is not registered", name)
	}
	r.tombstone[name] = c
	delete(r.live, name)
	return nil
}

// Snapshot 返回按名称升序排列的一致快照（存活集群 + 删除墓碑）。
func (r *Registry) Snapshot() []*snapshotCluster {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snap := make([]*snapshotCluster, 0, len(r.live)+len(r.tombstone))
	for _, c := range r.live {
		snap = append(snap, &snapshotCluster{cluster: cloneCluster(c), dead: false})
	}
	for _, c := range r.tombstone {
		snap = append(snap, &snapshotCluster{cluster: cloneCluster(c), dead: true})
	}
	sort.Slice(snap, func(i, j int) bool {
		return snap[i].cluster.Name < snap[j].cluster.Name
	})
	return snap
}

// Allocate 在一致快照上执行纯函数分配。被拒绝的请求在进入计算前即返回，
// 计算过程不触碰登记表，因此任何结果（包括拒绝）都不会改变集群状态。
func (r *Registry) Allocate(total *big.Int) (*Result, error) {
	if total == nil {
		return nil, invalidParam("total replicas must not be nil")
	}
	if total.Sign() < 0 {
		return nil, invalidParam("total replicas must not be negative, got %s", total)
	}
	snap := r.Snapshot()
	targets, _, err := allocate(snap, total)
	if err != nil {
		return nil, err
	}
	return buildPlan(snap, targets), nil
}
