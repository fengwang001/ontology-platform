// Package queue 提供带队首惰性过期与溢出策略的有界队列。
package queue

import (
	"errors"
	"sync"
)

// Policy 是队列满时的溢出处理策略。
type Policy int

const (
	// DropHead 队列满时丢弃队首（记入死信，原因为溢出）后再入队。
	DropHead Policy = iota
	// Reject 队列满时整体拒绝入队，模拟清理不落地。
	Reject
)

// Reason 是消息进入死信的原因。
type Reason int

const (
	// ReasonExpired 消息在队首清理时被判定过期。
	ReasonExpired Reason = iota
	// ReasonOverflow 消息因队列满被丢弃队首策略驱逐。
	ReasonOverflow
)

// 可被调用方区分的拒绝原因。
var (
	ErrInvalidCapacity = errors.New("queue: 容量必须不小于 1")
	ErrNegativeTTL     = errors.New("queue: ttl 不能为负")
	ErrClockBackward   = errors.New("queue: now 小于此前已见的最大 now")
	ErrQueueFull       = errors.New("queue: 拒绝策略下清理后队列仍满")
	ErrEmptyQueue      = errors.New("queue: 清理后队列为空")
)

// DeadLetter 记录一条死信及其原因。
type DeadLetter struct {
	Message any
	Reason  Reason
}

type entry struct {
	message  any
	expireAt int64
}

// Queue 是有界队列，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Queue struct {
	mu      sync.Mutex
	cap     int
	policy  Policy
	items   []entry
	dead    []DeadLetter
	maxNow  int64
	seenNow bool
}

// New 构造容量为 capacity、溢出策略为 policy 的队列。
// capacity 小于 1 时返回 ErrInvalidCapacity。
func New(capacity int, policy Policy) (*Queue, error) {
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Queue{cap: capacity, policy: policy}, nil
}

// Enqueue 在时刻 now 入队一条存活期为 ttl 毫秒的消息，
// 到期时刻为 now+ttl，now 不小于到期时刻即视为过期。
//
// 入队时若长度等于容量，先模拟队首清理：清理后有空位则清理
// 落地并入队；仍满时 DropHead 策略把队首记入死信（溢出）后
// 入队，Reject 策略整体拒绝且清理不落地。
// 被拒绝时不改变队列、死信与已见最大 now。
func (q *Queue) Enqueue(message any, ttl, now int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkClock(now); err != nil {
		return err
	}
	if ttl < 0 {
		return ErrNegativeTTL
	}
	expired := q.headExpired(now)
	if len(q.items)-expired >= q.cap && q.policy == Reject {
		return ErrQueueFull
	}
	q.commitCleanup(expired)
	if len(q.items) >= q.cap {
		q.dead = append(q.dead, DeadLetter{Message: q.items[0].message, Reason: ReasonOverflow})
		q.items = q.items[1:]
	}
	q.items = append(q.items, entry{message: message, expireAt: now + ttl})
	q.advanceClock(now)
	return nil
}

// Dequeue 在时刻 now 先做队首清理，再返回队首消息。
// 清理后队空时返回 ErrEmptyQueue，且清理不落地。
func (q *Queue) Dequeue(now int64) (any, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.checkClock(now); err != nil {
		return nil, err
	}
	expired := q.headExpired(now)
	if len(q.items)-expired == 0 {
		return nil, ErrEmptyQueue
	}
	q.commitCleanup(expired)
	head := q.items[0]
	q.items = q.items[1:]
	q.advanceClock(now)
	return head.message, nil
}

// checkClock 校验时钟未倒退，调用方需持锁。
func (q *Queue) checkClock(now int64) error {
	if q.seenNow && now < q.maxNow {
		return ErrClockBackward
	}
	return nil
}

// advanceClock 记录已见最大 now，调用方需持锁。
func (q *Queue) advanceClock(now int64) {
	q.maxNow = now
	q.seenNow = true
}

// headExpired 返回从队首起连续过期（到期时刻不大于 now）
// 的消息条数，遇第一条未过期消息即停止，调用方需持锁。
func (q *Queue) headExpired(now int64) int {
	n := 0
	for n < len(q.items) && q.items[n].expireAt <= now {
		n++
	}
	return n
}

// commitCleanup 把队首 n 条过期消息移入死信（原因为过期），
// 同一次清理中多条按队列顺序记录，调用方需持锁。
func (q *Queue) commitCleanup(n int) {
	for _, e := range q.items[:n] {
		q.dead = append(q.dead, DeadLetter{Message: e.message, Reason: ReasonExpired})
	}
	q.items = q.items[n:]
}

// Len 返回队列长度，含全部未被清理的消息（含已过期但不在
// 连续队首过期段内者），不超过容量。
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// DeadLetters 按进入顺序返回死信快照。
func (q *Queue) DeadLetters() []DeadLetter {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]DeadLetter, len(q.dead))
	copy(out, q.dead)
	return out
}
