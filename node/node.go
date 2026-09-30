package node

import (
	"io"
	"log"
	"os"
	"sort"
	"sync"
)

// Kind 为容器承诺类型。
type Kind int

const (
	KindGuaranteed Kind = iota // r == l，保证型
	KindBurstable              // 0 < r < l，突发型
	KindBestEffort             // r == 0，尽力型
)

func (k Kind) String() string {
	switch k {
	case KindGuaranteed:
		return "guaranteed"
	case KindBurstable:
		return "burstable"
	case KindBestEffort:
		return "best-effort"
	default:
		return "unknown"
	}
}

// Eviction 记录一次被驱逐容器及其排序依据。
type Eviction struct {
	ID             string
	Kind           Kind
	Usage          int64
	Request        int64
	Limit          int64
	BurstableFirst bool  // 第一键：u > r 排在前面
	KindRank       int   // 第二键：尽力型 0、突发型 1、保证型 2（值小者先驱逐）
	BurstSize      int64 // 第三键：u - r
}

// ContainerInfo 为在册容器的查询视图。
type ContainerInfo struct {
	ID      string
	Request int64
	Limit   int64
	Usage   int64
	Kind    Kind
}

// Snapshot 为某一时刻的账目快照。
type Snapshot struct {
	Allocatable int64
	Oversell    int
	LimitCap    int64
	Requests    int64
	Limits      int64
	Usage       int64
	Containers  []ContainerInfo
}

// Node 为节点内存超卖准入与压力驱逐器。
// 所有操作在同一把互斥锁下串行化，保证任意时刻账目不变量成立，
// 且同一操作序列重放得到完全相同的驱逐序列。
type Node struct {
	mu          sync.Mutex
	allocatable int64
	oversell    int
	limitCap    int64
	requests    int64
	limits      int64
	usage       int64
	containers  map[string]*container
	logger      *log.Logger
}

type container struct {
	id      string
	request int64
	limit   int64
	usage   int64
	kind    Kind
}

// Option 配置 Node。
type Option func(*Node)

// WithLogWriter 重定向判定日志输出（默认 stderr）。
func WithLogWriter(w io.Writer) Option {
	return func(n *Node) {
		if w != nil {
			n.logger = log.New(w, "[node] ", log.LstdFlags|log.Lmicroseconds)
		}
	}
}

// New 构造一个驱逐器：A 为可分配内存，O 为整数超卖倍数。
// A 不为正或 O 小于 1 时整体拒绝并返回可区分原因。
func New(allocatable int64, oversell int, opts ...Option) (*Node, error) {
	if allocatable <= 0 {
		return nil, ErrInvalidAllocatable
	}
	if oversell < 1 {
		return nil, ErrInvalidOversell
	}
	n := &Node{
		allocatable: allocatable,
		oversell:    oversell,
		limitCap:    allocatable * int64(oversell),
		containers:  make(map[string]*container),
		logger:      log.New(os.Stderr, "[node] ", log.LstdFlags|log.Lmicroseconds),
	}
	for _, opt := range opts {
		opt(n)
	}
	n.logger.Printf("INIT input={A=%d O=%d} 判定依据: limitCap=O*A=%d", allocatable, oversell, n.limitCap)
	return n, nil
}

// Admit 准入一个容器；失败时返回可区分原因且不改动账目。
func (n *Node) Admit(id string, request, limit int64) (err error) {
	// 先做不依赖账目的入参校验，被拒绝的操作不得触碰任何账目。
	if request < 0 {
		n.logger.Printf("ADMIT input={id=%s r=%d l=%d} -> REJECT 原因=%v", id, request, limit, ErrNegativeRequest)
		return ErrNegativeRequest
	}
	if limit <= 0 || limit < request {
		n.logger.Printf("ADMIT input={id=%s r=%d l=%d} -> REJECT 原因=%v", id, request, limit, ErrInvalidLimit)
		return ErrInvalidLimit
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	defer func() {
		if err != nil {
			n.logger.Printf("ADMIT input={id=%s r=%d l=%d} -> DENY 原因=%v 账目不变 {sumR=%d sumL=%d}",
				id, request, limit, err, n.requests, n.limits)
		}
	}()

	if _, exists := n.containers[id]; exists {
		return ErrDuplicateID
	}
	// 两类准入条件同时不满足时，先报请求不足。
	if n.requests+request > n.allocatable {
		err = ErrRequestExhausted
		return err
	}
	if n.limits+limit > n.limitCap {
		err = ErrLimitOversold
		return err
	}

	c := &container{id: id, request: request, limit: limit, kind: kindOf(request, limit)}
	n.containers[id] = c
	n.requests += request
	n.limits += limit
	n.logger.Printf("ADMIT input={id=%s r=%d l=%d kind=%s} -> ADMIT 判定依据: sumR=%d<=%d 且 sumL=%d<=%d",
		id, request, limit, c.kind, n.requests, n.allocatable, n.limits, n.limitCap)
	return nil
}

// Report 上报用量并在超压时按固定次序驱逐，返回被驱逐标识序列及依据。
func (n *Node) Report(id string, usage int64) (evicted []Eviction, err error) {
	if usage < 0 {
		n.logger.Printf("REPORT input={id=%s u=%d} -> REJECT 原因=%v", id, usage, ErrInvalidUsage)
		return nil, ErrInvalidUsage
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	c, ok := n.containers[id]
	if !ok {
		n.logger.Printf("REPORT input={id=%s u=%d} -> REJECT 原因=%v", id, usage, ErrContainerNotFound)
		return nil, ErrContainerNotFound
	}
	if usage > c.limit {
		n.logger.Printf("REPORT input={id=%s u=%d} -> REJECT 原因=%v (limit=%d)", id, usage, ErrInvalidUsage, c.limit)
		return nil, ErrInvalidUsage
	}

	n.usage += usage - c.usage
	c.usage = usage
	n.logger.Printf("REPORT input={id=%s u=%d} 记账后 sumU=%d (A=%d)", id, usage, n.usage, n.allocatable)

	evicted = n.enforcePressure(id)
	n.logger.Printf("REPORT input={id=%s u=%d} -> OUTPUT evicted=%v 终止依据: sumU=%d<=%d 达标即停",
		id, usage, evictionIDs(evicted), n.usage, n.allocatable)
	return evicted, nil
}

// Delete 删除在册容器，其请求与上限即刻退出账目。
func (n *Node) Delete(id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	c, ok := n.containers[id]
	if !ok {
		n.logger.Printf("DELETE input={id=%s} -> REJECT 原因=%v", id, ErrContainerNotFound)
		return ErrContainerNotFound
	}
	delete(n.containers, id)
	n.requests -= c.request
	n.limits -= c.limit
	n.usage -= c.usage
	n.logger.Printf("DELETE input={id=%s} -> OK r/l/u 即刻退出账目 {sumR=%d sumL=%d sumU=%d}",
		id, n.requests, n.limits, n.usage)
	return nil
}

// Query 返回当前账目快照（按标识升序）。
func (n *Node) Query() Snapshot {
	n.mu.Lock()
	defer n.mu.Unlock()

	infos := make([]ContainerInfo, 0, len(n.containers))
	for _, c := range n.containers {
		infos = append(infos, ContainerInfo{
			ID:      c.id,
			Request: c.request,
			Limit:   c.limit,
			Usage:   c.usage,
			Kind:    c.kind,
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })

	s := Snapshot{
		Allocatable: n.allocatable,
		Oversell:    n.oversell,
		LimitCap:    n.limitCap,
		Requests:    n.requests,
		Limits:      n.limits,
		Usage:       n.usage,
		Containers:  infos,
	}
	n.logger.Printf("QUERY -> {sumR=%d sumL=%d sumU=%d 在册=%d}", s.Requests, s.Limits, s.Usage, len(infos))
	return s
}

// enforcePressure 在调用方持锁状态下按固定次序驱逐，直到用量之和不超过 A。
// 次序在整个驱逐过程开始时一次性确定，随后逐个驱逐、达标即停。
func (n *Node) enforcePressure(trigger string) []Eviction {
	if n.usage <= n.allocatable {
		return nil
	}

	order := make([]*container, 0, len(n.containers))
	for _, c := range n.containers {
		order = append(order, c)
	}
	sort.Slice(order, func(i, j int) bool {
		return evictionLess(order[i], order[j])
	})

	var evicted []Eviction
	for _, c := range order {
		if n.usage <= n.allocatable {
			break
		}
		// 排序后容器集合未被并发改动（持锁），且本次只做删除；
		// 若前序驱逐已移除它则跳过，保持次序固定。
		if cur, ok := n.containers[c.id]; !ok || cur != c {
			continue
		}
		e := Eviction{
			ID:             c.id,
			Kind:           c.kind,
			Usage:          c.usage,
			Request:        c.request,
			Limit:          c.limit,
			BurstableFirst: c.usage > c.request,
			KindRank:       kindRank(c.kind),
			BurstSize:      c.usage - c.request,
		}
		delete(n.containers, c.id)
		n.requests -= c.request
		n.limits -= c.limit
		n.usage -= c.usage
		evicted = append(evicted, e)
		n.logger.Printf("EVICT 触发={%s} 次序依据: key1[超发=%v] key2[kindRank=%d %s] key3[u-r=%d] key4[id=%s] -> 驱逐后 sumU=%d",
			trigger, e.BurstableFirst, e.KindRank, e.Kind, e.BurstSize, e.ID, n.usage)
	}
	return evicted
}

// evictionLess 实现四个排序键：
// 1) u>r 者先于 u<=r 者；
// 2) 尽力型先于突发型先于保证型；
// 3) u-r 大者先；
// 4) 标识升序（保证重放确定性）。
func evictionLess(a, b *container) bool {
	ab, bb := a.usage > a.request, b.usage > b.request
	if ab != bb {
		return ab
	}
	ar, br := kindRank(a.kind), kindRank(b.kind)
	if ar != br {
		return ar < br
	}
	ad, bd := a.usage-a.request, b.usage-b.request
	if ad != bd {
		return ad > bd
	}
	return a.id < b.id
}

func kindOf(request, limit int64) Kind {
	switch {
	case request == 0:
		return KindBestEffort
	case request == limit:
		return KindGuaranteed
	default:
		return KindBurstable
	}
}

func kindRank(k Kind) int {
	switch k {
	case KindBestEffort:
		return 0
	case KindBurstable:
		return 1
	default:
		return 2
	}
}

func evictionIDs(es []Eviction) []string {
	ids := make([]string, 0, len(es))
	for _, e := range es {
		ids = append(ids, e.ID)
	}
	return ids
}
