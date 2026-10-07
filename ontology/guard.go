package ontology

import (
	"errors"
	"sync"
	"time"
)

// Clock 抽象时间源，测试中使用可控假时钟。
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Link 是一条已确认的关联关系（去重后的事实）。
type Link struct {
	SourceID string
	TargetID string
}

// reservation 是一个进行中请求对名额的占用。
type reservation struct {
	requestID string
	source    string
	target    string
	deadline  time.Time
	observed  int64
	heapIndex int
}

// deadlineHeap 是按 deadline 升序的最小堆。
type deadlineHeap []*reservation

func (h deadlineHeap) Len() int           { return len(h) }
func (h deadlineHeap) Less(i, j int) bool { return h[i].deadline.Before(h[j].deadline) }
func (h deadlineHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *deadlineHeap) Push(x any) {
	r := x.(*reservation)
	r.heapIndex = len(*h)
	*h = append(*h, r)
}

func (h *deadlineHeap) Pop() any {
	old := *h
	n := len(old)
	r := old[n-1]
	old[n-1] = nil
	r.heapIndex = -1
	*h = old[:n-1]
	return r
}

// scope 保存单个基数约束作用域的全部可变状态。
// 所有判定与状态迁移都在 Guard.mu 下原子完成，保证可线性化。
type scope struct {
	key      ScopeKey
	capacity int
	version  int64 // 仅成功提交递增；拒绝/回滚/过期不动
	links    map[Link]struct{}
	pending  map[string]*reservation
	deadlineHeap
	outcomes map[string]Outcome
}

func newScope(key ScopeKey, capacity int) *scope {
	return &scope{
		key:      key,
		capacity: capacity,
		links:    make(map[Link]struct{}),
		pending:  make(map[string]*reservation),
		outcomes: make(map[string]Outcome),
	}
}

// Decision 是一次准入判定的返回。
type Decision struct {
	Admitted  bool
	Reason    RejectReason
	Version   int64 // 判定时作用域版本
	Confirmed int
	InFlight  int
	Seq       int64 // 决策日志序列号（证据，不改变业务状态）
}

// CommitResult 是提交结果。
type CommitResult struct {
	OK        bool
	Version   int64
	Reason    RejectReason
	Outcome   Outcome
	Confirmed int
	InFlight  int
}

// BeginRequest 是新建关联请求的入参。
type BeginRequest struct {
	RequestID       string // 全局唯一
	Scope           ScopeKey
	SourceID        string
	TargetID        string
	ObservedVersion int64 // 调用方读取名额时看到的基线版本
}

var (
	ErrScopeNotFound = errors.New("ontology: scope not registered")
	ErrCapacityInUse = errors.New("ontology: cannot change capacity of existing scope")
	ErrInvalidCap    = errors.New("ontology: capacity must be non-negative")
)

// Guard 是基数约束并发名额控制器。
//
// 关键取舍：用一个互斥锁作为所有判定的线性化点，而不是按作用域分片。
// 临界区内只做与进行中请求数相关的内存操作，不做 I/O；正确性可直接论证，
// 且判定开销不随已确认关联总数或历史请求数增长（见设计说明）。
type Guard struct {
	mu       sync.Mutex
	scopes   map[ScopeKey]*scope
	byReq    map[string]*scope // requestID -> 作用域，O(1) 定位
	journal  *Journal
	clock    Clock
	leaseTTL time.Duration
}

// Config 用于构造 Guard。
type Config struct {
	Clock    Clock
	LeaseTTL time.Duration
}

func NewGuard(cfg Config) *Guard {
	clk := cfg.Clock
	if clk == nil {
		clk = systemClock{}
	}
	ttl := cfg.LeaseTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Guard{
		scopes:   make(map[ScopeKey]*scope),
		byReq:    make(map[string]*scope),
		journal:  NewJournal(),
		clock:    clk,
		leaseTTL: ttl,
	}
}

// EnsureScope 注册一个作用域的基数上限；已存在时容量必须一致。
func (g *Guard) EnsureScope(key ScopeKey, capacity int) error {
	if capacity < 0 {
		return ErrInvalidCap
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.scopes[key]; ok {
		if s.capacity != capacity {
			return ErrCapacityInUse
		}
		return nil
	}
	g.scopes[key] = newScope(key, capacity)
	return nil
}

func (g *Guard) Journal() *Journal { return g.journal }
