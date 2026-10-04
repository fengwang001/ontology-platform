// Package envlock 维护单个环境的互斥锁、当前版本与先进先出等待队列。
package envlock

// Outcome 是队首请求在授锁时刻 g 被复核后的判定。
type Outcome int

const (
	// OutcomeStale 版本已过时：普通 ver<=cur，回滚 ver>=cur。
	OutcomeStale Outcome = iota
	// OutcomeExpired 版本仍可部署，但在 g 时刻有效批准数不足。
	OutcomeExpired
	// OutcomeLocked 复核通过，请求在 g 时刻取得环境锁。
	OutcomeLocked
)

// Queued 是等待队列中的一项。
type Queued struct {
	ID       int64
	Ver      int64
	Rollback bool
}

// Env 是单个环境的全部锁状态。
// 持锁请求 ID 为 0 表示空闲；任何时刻至多一个持锁者。
type Env struct {
	name      string
	cur       int64
	succeeded map[int64]struct{}
	queue     []Queued
	holder    int64

	holderVer      int64
	holderRollback bool
}

// New 创建名为 name、初始当前版本 0、成功版本集合为空的环境锁。
func New(name string) *Env {
	return &Env{name: name, succeeded: make(map[int64]struct{})}
}

// Name 返回环境名。
func (e *Env) Name() string { return e.name }

// Cur 返回当前已部署版本。
func (e *Env) Cur() int64 { return e.cur }

// Succeeded 报告 ver 是否曾在本环境成功部署。
func (e *Env) Succeeded(ver int64) bool {
	_, ok := e.succeeded[ver]
	return ok
}

// Holder 返回持锁请求 ID，0 表示空闲。
func (e *Env) Holder() int64 { return e.holder }

// QueueLen 返回等待队列长度。
func (e *Env) QueueLen() int { return len(e.queue) }

// Enqueue 把请求按转入 Queued 的次序排到队尾。
func (e *Env) Enqueue(q Queued) {
	e.queue = append(e.queue, q)
}

// Remove 把已在队中的请求移出队列（请求被取消时调用）。
func (e *Env) Remove(id int64) {
	for i, q := range e.queue {
		if q.ID == id {
			e.queue = append(e.queue[:i], e.queue[i+1:]...)
			return
		}
	}
}

// MarkSucceeded 记录 ver 已成功部署，并把 cur 推进到 ver。
func (e *Env) MarkSucceeded(ver int64) {
	e.cur = ver
	e.succeeded[ver] = struct{}{}
}

// TryTake 在逻辑时刻 g 反复复核队首，直到队列空、批准不足或成功授锁。
// stale 与 expired 收集被移出队列的请求 ID；validVotes 给出某请求在 g 时刻
// 的有效批准数（need>0 的请求才会被查询）。
// 环境必须当前空闲；授锁成功时持锁者记录于 e。
func (e *Env) TryTake(validVotes func(id int64) int, needOf func(id int64) int, stale, expired *[]int64) {
	for len(e.queue) > 0 {
		head := e.queue[0]
		if (!head.Rollback && head.Ver <= e.cur) || (head.Rollback && head.Ver >= e.cur) {
			e.queue = e.queue[1:]
			*stale = append(*stale, head.ID)
			continue
		}
		if needOf(head.ID) > 0 && validVotes(head.ID) < needOf(head.ID) {
			e.queue = e.queue[1:]
			*expired = append(*expired, head.ID)
			continue
		}
		e.queue = e.queue[1:]
		e.holder = head.ID
		e.holderVer = head.Ver
		e.holderRollback = head.Rollback
		return
	}
}

// Release 清空持锁者（运行失败、取消或超时后调用）。
func (e *Env) Release() {
	e.holder = 0
	e.holderVer = 0
	e.holderRollback = false
}

// HolderVer 返回持锁请求的版本。
func (e *Env) HolderVer() int64 { return e.holderVer }
