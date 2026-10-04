// Package action 保存按动作摘要索引的在途操作、等待者与结果缓存。
package action

import "errors"

// 六类可由 errors.Is 区分的拒绝原因。
var (
	ErrInvalid      = errors.New("invalid argument")
	ErrNotFound     = errors.New("not found")
	ErrExists       = errors.New("already exists")
	ErrState        = errors.New("state mismatch")
	ErrAttemptStale = errors.New("attempt expired")
	ErrNoFreeSlot   = errors.New("no free slot")
)

// KV 是 platform / props 中的单个键值对。
type KV struct {
	Key   string
	Value string
}

// Platform 为有序的键值约束（0 到 8 对）。
type Platform []KV

// State 为操作生命周期状态。
type State int

const (
	Queued State = iota
	Assigned
	Abandoned
	Done
)

// Kind 为等待者收到的终局种类。
type Kind int

const (
	Cached Kind = iota + 1
	Result
	Cancelled
	Lost
)

// Outcome 是等待者收到的唯一终局。
type Outcome struct {
	Kind Kind
	Exit int
}

// Waiter 是一个附着在操作上的等待者。
type Waiter struct {
	ID   int
	Prio int

	done bool
	ch   chan Outcome
}

// Op 为一个按摘要合并的在途操作。
type Op struct {
	Digest   string
	Platform Platform
	Seq      int
	State    State
	Losses   int

	Waiters map[int]*Waiter
}

// NewWaiter 创建一个等待者。
func NewWaiter(id, prio int) *Waiter {
	return &Waiter{ID: id, Prio: prio, ch: make(chan Outcome, 1)}
}

// Done 返回终局通道（恰好收到一次）。
func (w *Waiter) Done() <-chan Outcome { return w.ch }

// Terminal 报告等待者是否已收终局。
func (w *Waiter) Terminal() bool { return w.done }

// Finish 投递终局；重复调用不生效。
func (w *Waiter) Finish(o Outcome) bool {
	if w.done {
		return false
	}
	w.done = true
	w.ch <- o
	return true
}

// NewOp 创建在途操作。
func NewOp(digest string, p Platform, seq int, first *Waiter) *Op {
	return &Op{
		Digest:   digest,
		Platform: p,
		Seq:      seq,
		State:    Queued,
		Waiters:  map[int]*Waiter{first.ID: first},
	}
}

// EffectivePrio 返回现存等待者 prio 的最大值；无等待者返回 -1。
func (o *Op) EffectivePrio() int {
	max := -1
	for _, w := range o.Waiters {
		if w.Prio > max {
			max = w.Prio
		}
	}
	return max
}

// SubsetOf 报告操作 platform 的每对键值是否都在 props 中相等出现。
func (p Platform) SubsetOf(props []KV) bool {
	have := make(map[string]string, len(props))
	for _, kv := range props {
		have[kv.Key] = kv.Value
	}
	for _, kv := range p {
		if v, ok := have[kv.Key]; !ok || v != kv.Value {
			return false
		}
	}
	return true
}

// Equal 报告两个 platform 是否完全相同（顺序无关）。
func (p Platform) Equal(other Platform) bool {
	return p.SubsetOf(other) && other.SubsetOf(p)
}

// Cache 是 digest -> 退出码的结果缓存。
type Cache struct {
	values map[string]int
}

// NewCache 创建空缓存。
func NewCache() *Cache { return &Cache{values: make(map[string]int)} }

// Get 命中返回退出码与 true。
func (c *Cache) Get(digest string) (int, bool) {
	exit, ok := c.values[digest]
	return exit, ok
}

// Put 写入（覆盖）结果。
func (c *Cache) Put(digest string, exit int) { c.values[digest] = exit }

// Snapshot 返回缓存内容的拷贝。
func (c *Cache) Snapshot() map[string]int {
	out := make(map[string]int, len(c.values))
	for k, v := range c.values {
		out[k] = v
	}
	return out
}

// LookupStats 统计一次定位过程中的查找次数。
type LookupStats struct {
	Cache    int
	InFlight int
}

// Registry 是 digest -> 在途操作的索引。
type Registry struct {
	ops     map[string]*Op
	lookups LookupStats
}

// NewRegistry 创建空索引。
func NewRegistry() *Registry { return &Registry{ops: make(map[string]*Op)} }

// LookupCache 统计一次缓存查找。
func (r *Registry) LookupCache(c *Cache, digest string) (int, bool) {
	r.lookups.Cache++
	return c.Get(digest)
}

// LookupInFlight 统计一次在途查找。
func (r *Registry) LookupInFlight(digest string) (*Op, bool) {
	r.lookups.InFlight++
	o, ok := r.ops[digest]
	return o, ok
}

// Add 登记在途操作。
func (r *Registry) Add(o *Op) { r.ops[o.Digest] = o }

// Delete 移除在途操作。
func (r *Registry) Delete(digest string) { delete(r.ops, digest) }

// Get 不计查找次数地读取在途操作。
func (r *Registry) Get(digest string) (*Op, bool) {
	o, ok := r.ops[digest]
	return o, ok
}

// All 返回全部在途操作（不计查找次数，供内部遍历）。
func (r *Registry) All() []*Op {
	out := make([]*Op, 0, len(r.ops))
	for _, o := range r.ops {
		out = append(out, o)
	}
	return out
}

// Lookups 返回并清零自上次以来的查找计数。
func (r *Registry) Lookups() LookupStats {
	s := r.lookups
	r.lookups = LookupStats{}
	return s
}
