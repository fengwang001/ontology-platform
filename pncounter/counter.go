// Package pncounter 实现可增可减的无冲突复制计数器（PN-Counter CvRDT）。
package pncounter

import (
	"math"
	"math/big"
	"sync"
)

// DefaultLimit 是不显式指定上限时，单个计数分量允许达到的最大值。
const DefaultLimit uint64 = math.MaxUint64

// Cluster 持有固定数量的副本，负责副本的编号与统一上限。
type Cluster struct {
	limit    uint64
	replicas []*Replica
}

// Replica 是一个计数器副本：两份非负计数向量分别记录增与减。
type Replica struct {
	id      int
	cluster *Cluster
	limit   uint64

	mu  sync.Mutex
	inc []uint64
	dec []uint64
}

// Snapshot 是某一副本在某一时刻的不可变状态副本。
type Snapshot struct {
	ID  int
	Inc []uint64
	Dec []uint64
}

// NewCluster 创建含 n 个副本、使用 DefaultLimit 的集群。
func NewCluster(n int) (*Cluster, error) {
	return NewClusterWithLimit(n, DefaultLimit)
}

// NewClusterWithLimit 创建含 n 个副本、单分量上限为 limit 的集群。
func NewClusterWithLimit(n int, limit uint64) (*Cluster, error) {
	if n <= 0 {
		return nil, ErrInvalidReplicaCount
	}
	if limit == 0 {
		return nil, ErrInvalidLimit
	}
	c := &Cluster{limit: limit, replicas: make([]*Replica, n)}
	for i := range n {
		c.replicas[i] = &Replica{
			id:      i,
			cluster: c,
			limit:   limit,
			inc:     make([]uint64, n),
			dec:     make([]uint64, n),
		}
	}
	return c, nil
}

// Size 返回集群中的副本数量。
func (c *Cluster) Size() int { return len(c.replicas) }

// Limit 返回集群配置的单分量上限。
func (c *Cluster) Limit() uint64 { return c.limit }

// Replica 按编号取出集群中的副本；编号不存在返回 ErrNoSuchReplica。
func (c *Cluster) Replica(id int) (*Replica, error) {
	if id < 0 || id >= len(c.replicas) {
		return nil, ErrNoSuchReplica
	}
	return c.replicas[id], nil
}

// ID 返回副本编号。
func (r *Replica) ID() int { return r.id }

// Increment 在本副本自己的增分量上加 delta；非法输入整体拒绝。
func (r *Replica) Increment(delta uint64) error {
	if delta == 0 {
		return ErrNonPositiveDelta
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if delta > r.limit || r.inc[r.id] > r.limit-delta {
		return ErrComponentOverflow
	}
	r.inc[r.id] += delta
	return nil
}

// Decrement 在本副本自己的减分量上加 delta；非法输入整体拒绝。
func (r *Replica) Decrement(delta uint64) error {
	if delta == 0 {
		return ErrNonPositiveDelta
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if delta > r.limit || r.dec[r.id] > r.limit-delta {
		return ErrComponentOverflow
	}
	r.dec[r.id] += delta
	return nil
}

// Merge 以 other 为来源合并进接收者 r：逐分量取较大者，只改 r。
// r 与 other 为同一副本是合法空操作。
func (r *Replica) Merge(other *Replica) error {
	if other == nil {
		return ErrNoSuchReplica
	}
	// 固定加锁顺序：先锁编号小的副本，再锁编号大的。
	// 这样任意两个副本之间互逆方向的合并同时进行也不会形成环路、不会死锁。
	first, second := r, other
	if other.id < r.id {
		first, second = other, r
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	if second != first {
		second.mu.Lock()
		defer second.mu.Unlock()
	}

	if len(r.inc) != len(other.inc) || len(r.dec) != len(other.dec) ||
		len(r.inc) != len(r.dec) {
		return ErrInvariantViolation
	}
	n := len(r.inc)
	// 合并不可能让任何分量超过 limit：取两个合法值的较大者仍 <= limit。
	for i := range n {
		if other.inc[i] > r.inc[i] {
			r.inc[i] = other.inc[i]
		}
		if other.dec[i] > r.dec[i] {
			r.dec[i] = other.dec[i]
		}
	}
	return nil
}

// Value 返回当前值：增向量之和减去减向量之和（可能为负）。
func (r *Replica) Value() *big.Int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return valueOf(r.inc, r.dec)
}

// Snapshot 返回当前状态的防御性拷贝。
func (r *Replica) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	inc := make([]uint64, len(r.inc))
	dec := make([]uint64, len(r.dec))
	copy(inc, r.inc)
	copy(dec, r.dec)
	return Snapshot{ID: r.id, Inc: inc, Dec: dec}
}

// Check 校验该副本的内部不变量。
func (r *Replica) Check() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.id < 0 || r.cluster == nil || r.limit == 0 {
		return ErrInvariantViolation
	}
	if len(r.inc) != len(r.dec) || r.id >= len(r.inc) {
		return ErrInvariantViolation
	}
	for i := range r.inc {
		if r.inc[i] > r.limit || r.dec[i] > r.limit {
			return ErrComponentOverflow
		}
	}
	return nil
}

// Check 校验集群内所有副本的内部不变量。
func (c *Cluster) Check() error {
	if len(c.replicas) == 0 || c.limit == 0 {
		return ErrInvariantViolation
	}
	for _, r := range c.replicas {
		if r == nil || r.cluster != c {
			return ErrInvariantViolation
		}
		if err := r.Check(); err != nil {
			return err
		}
	}
	return nil
}

// Values 返回所有副本当前值的快照。
func (c *Cluster) Values() []*big.Int {
	values := make([]*big.Int, len(c.replicas))
	for i, r := range c.replicas {
		values[i] = r.Value()
	}
	return values
}

// valueOf 计算 sum(inc) - sum(dec)。用 big.Int 累加，避免求和溢出，
// 结果可为负，不做任何截断。
func valueOf(inc, dec []uint64) *big.Int {
	sum := new(big.Int)
	for _, v := range inc {
		sum.Add(sum, new(big.Int).SetUint64(v))
	}
	for _, v := range dec {
		sum.Sub(sum, new(big.Int).SetUint64(v))
	}
	return sum
}
