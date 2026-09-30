// Package memory 实现节点内存的超卖准入与压力驱逐器。
//
// 准入条件（两类，同时校验）：
//  1. 在册请求之和 + r <= A（否则 ErrInsufficientRequest）
//  2. 在册上限之和 + l <= O*A（否则 ErrOversellLimitExceeded）
//
// 每次上报后若在册用量之和超过 A，按固定次序驱逐直到达标即停：
//  1. u > r 者先于 u <= r 者
//  2. 尽力型先于突发型先于保证型
//  3. u - r 大的先
//  4. 标识升序
package memory

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的拒绝原因。
var (
	ErrInvalidAllocatable    = errors.New("memory: allocatable must be positive")
	ErrInvalidOversell       = errors.New("memory: oversell factor must be >= 1")
	ErrNegativeRequest       = errors.New("memory: request must not be negative")
	ErrInvalidLimit          = errors.New("memory: limit must be positive and >= request")
	ErrDuplicateID           = errors.New("memory: container id already exists")
	ErrNotFound              = errors.New("memory: container not found")
	ErrInvalidUsage          = errors.New("memory: usage must be in [0, limit]")
	ErrInsufficientRequest   = errors.New("memory: insufficient request headroom")
	ErrOversellLimitExceeded = errors.New("memory: oversell limit exceeded")
)

// Class 容器类别。
type Class int

const (
	BestEffort Class = iota // r == 0
	Burstable               // 0 < r < l
	Guaranteed              // r == l
)

func (c Class) String() string {
	switch c {
	case BestEffort:
		return "besteffort"
	case Burstable:
		return "burstable"
	case Guaranteed:
		return "guaranteed"
	}
	return "unknown"
}

func classOf(request, limit int64) Class {
	switch {
	case request == 0:
		return BestEffort
	case request == limit:
		return Guaranteed
	default:
		return Burstable
	}
}

// Eviction 一次驱逐的记录，含次序依据。
type Eviction struct {
	ID     string
	Reason string
}

type container struct {
	id             string
	request, limit int64
	usage          int64
	class          Class
}

// Stats 节点账目快照。
type Stats struct {
	Allocatable int64
	Oversell    int64
	SumRequest  int64
	SumLimit    int64
	SumUsage    int64
	Containers  int
}

// Node 节点内存准入与驱逐器，并发安全。
type Node struct {
	mu          sync.Mutex
	allocatable int64
	oversell    int64
	containers  map[string]*container
	sumRequest  int64
	sumLimit    int64
	sumUsage    int64
}

// NewNode 创建节点。allocatable 必须为正，oversell 必须 >= 1。
func NewNode(allocatable, oversell int64) (*Node, error) {
	if allocatable <= 0 {
		return nil, ErrInvalidAllocatable
	}
	if oversell < 1 {
		return nil, ErrInvalidOversell
	}
	return &Node{
		allocatable: allocatable,
		oversell:    oversell,
		containers:  make(map[string]*container),
	}, nil
}

// Admit 准入一个容器。任一校验失败都不改变账目。
func (n *Node) Admit(id string, request, limit int64) error {
	if request < 0 {
		return ErrNegativeRequest
	}
	if limit <= 0 || limit < request {
		return ErrInvalidLimit
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.containers[id]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateID, id)
	}
	if n.sumRequest+request > n.allocatable {
		return fmt.Errorf("%w: sumRequest %d + %d > allocatable %d",
			ErrInsufficientRequest, n.sumRequest, request, n.allocatable)
	}
	if n.sumLimit+limit > n.oversell*n.allocatable {
		return fmt.Errorf("%w: sumLimit %d + %d > oversell %d x allocatable %d",
			ErrOversellLimitExceeded, n.sumLimit, limit, n.oversell, n.allocatable)
	}
	n.containers[id] = &container{
		id:      id,
		request: request,
		limit:   limit,
		class:   classOf(request, limit),
	}
	n.sumRequest += request
	n.sumLimit += limit
	return nil
}

// Report 上报容器当前用量。若在册用量之和超过 A，按固定次序驱逐
// 直到用量之和不超过 A 即停，返回被驱逐序列及次序依据。
func (n *Node) Report(id string, usage int64) ([]Eviction, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c, ok := n.containers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if usage < 0 || usage > c.limit {
		return nil, fmt.Errorf("%w: usage %d, limit %d", ErrInvalidUsage, usage, c.limit)
	}
	n.sumUsage += usage - c.usage
	c.usage = usage
	return n.evictLocked(), nil
}

// evictLocked 在持锁状态下执行驱逐，调用前需已更新用量。
func (n *Node) evictLocked() []Eviction {
	if n.sumUsage <= n.allocatable {
		return nil
	}
	ordered := make([]*container, 0, len(n.containers))
	for _, c := range n.containers {
		ordered = append(ordered, c)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if overA, overB := a.usage > a.request, b.usage > b.request; overA != overB {
			return overA
		}
		if a.class != b.class {
			return a.class < b.class
		}
		if exA, exB := a.usage-a.request, b.usage-b.request; exA != exB {
			return exA > exB
		}
		return a.id < b.id
	})
	var evicted []Eviction
	for _, c := range ordered {
		if n.sumUsage <= n.allocatable {
			break
		}
		reason := fmt.Sprintf("over-request=%t (u=%d r=%d), class=%s, excess u-r=%d, id=%q",
			c.usage > c.request, c.usage, c.request, c.class, c.usage-c.request, c.id)
		delete(n.containers, c.id)
		n.sumRequest -= c.request
		n.sumLimit -= c.limit
		n.sumUsage -= c.usage
		evicted = append(evicted, Eviction{ID: c.id, Reason: reason})
	}
	return evicted
}

// Delete 删除在册容器，其请求、上限与用量即刻退出账目。
func (n *Node) Delete(id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	c, ok := n.containers[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	delete(n.containers, id)
	n.sumRequest -= c.request
	n.sumLimit -= c.limit
	n.sumUsage -= c.usage
	return nil
}

// Snapshot 返回当前账目快照。
func (n *Node) Snapshot() Stats {
	n.mu.Lock()
	defer n.mu.Unlock()
	return Stats{
		Allocatable: n.allocatable,
		Oversell:    n.oversell,
		SumRequest:  n.sumRequest,
		SumLimit:    n.sumLimit,
		SumUsage:    n.sumUsage,
		Containers:  len(n.containers),
	}
}
