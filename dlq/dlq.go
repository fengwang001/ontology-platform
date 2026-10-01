// Package dlq 提供按原入队序重放的死信重放器。
//
// 系统包含若干命名队列（容量均为 C）与一个共享死信区。
// 每条消息在入队时获得全局入队序号 oseq（从 1 起，终身不变）；
// 进入死信区时获得死亡序号 dseq（从 1 起，按进入先后分配）。
// 重放按 oseq 升序（而非死亡序）把条目放回目标队列队尾，
// 单条消息累计重放次数达到上限 R 后被跳过且不占名额。
package dlq

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// RejectReason 区分重放被整体拒绝的原因。
type RejectReason string

const (
	// ReasonInvalidArgument：n 小于 1 或队列名为空。
	ReasonInvalidArgument RejectReason = "invalid_argument"
	// ReasonQueueNotFound：该队列不存在。
	ReasonQueueNotFound RejectReason = "queue_not_found"
	// ReasonNoDeadEntries：死信区中没有该队列的条目。
	ReasonNoDeadEntries RejectReason = "no_dead_entries"
	// ReasonAllExhausted：有条目但全部已达重放上限。
	ReasonAllExhausted RejectReason = "all_exhausted"
	// ReasonQueueFull：目标队列已满，一条也放不进。
	ReasonQueueFull RejectReason = "queue_full"
)

// ErrRejected 是所有 RejectError 的哨兵，可用 errors.Is 匹配。
var ErrRejected = errors.New("dlq: operation rejected")

// RejectError 表示一次被整体拒绝的操作；被拒绝的操作不改变任何状态。
type RejectError struct {
	Reason  RejectReason
	Queue   string
	Message string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("dlq: rejected (%s): %s", e.Reason, e.Message)
}

// Is 支持 errors.Is(err, ErrRejected) 判断。
func (e *RejectError) Is(target error) bool { return target == ErrRejected }

// Message 是队列/死信区中的消息视图。
type Message struct {
	OSeq    int64  // 全局入队序号，从 1 起，终身不变
	Body    string // 消息内容
	Replays int    // 累计重放次数
}

// DeadEntry 是死信区条目视图。
type DeadEntry struct {
	Message
	DSeq int64 // 死亡序号，本次进入死信区的先后，从 1 起
}

// System 是并发安全的队列 + 死信区系统。
type System struct {
	mu          sync.Mutex
	capacity    int
	replayLimit int
	nextOSeq    int64
	nextDSeq    int64
	queues      map[string][]*message
	dead        []*deadEntry
}

type message struct {
	oseq    int64
	body    string
	replays int
}

type deadEntry struct {
	msg   *message
	queue string // 来源（目标）队列名
	dseq  int64
}

// New 创建系统：每个命名队列容量为 capacity，单条消息重放上限为 replayLimit。
func New(capacity, replayLimit int) *System {
	if capacity < 1 {
		capacity = 1
	}
	if replayLimit < 1 {
		replayLimit = 1
	}
	return &System{
		capacity:    capacity,
		replayLimit: replayLimit,
		nextOSeq:    1,
		nextDSeq:    1,
		queues:      make(map[string][]*message),
	}
}

// AddQueue 注册一个命名队列；重复注册同名队列返回 false。
func (s *System) AddQueue(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return false
	}
	if _, ok := s.queues[name]; ok {
		return false
	}
	s.queues[name] = nil
	return true
}

// Enqueue 把消息放入队尾，返回全局入队序号 oseq（从 1 起）。
// 队列不存在或已满时拒绝，状态不变。
func (s *System) Enqueue(queue, body string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.queues[queue]
	if !ok {
		return 0, &RejectError{Reason: ReasonQueueNotFound, Queue: queue, Message: "enqueue: queue does not exist"}
	}
	if len(q) >= s.capacity {
		return 0, &RejectError{Reason: ReasonQueueFull, Queue: queue, Message: "enqueue: queue is full"}
	}
	oseq := s.nextOSeq
	s.nextOSeq++
	s.queues[queue] = append(q, &message{oseq: oseq, body: body})
	return oseq, nil
}

// Dequeue 弹出队首消息；队列不存在或为空时拒绝。
func (s *System) Dequeue(queue string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.queues[queue]
	if !ok {
		return Message{}, &RejectError{Reason: ReasonQueueNotFound, Queue: queue, Message: "dequeue: queue does not exist"}
	}
	if len(q) == 0 {
		return Message{}, &RejectError{Reason: ReasonNoDeadEntries, Queue: queue, Message: "dequeue: queue is empty"}
	}
	m := q[0]
	s.queues[queue] = q[1:]
	return Message{OSeq: m.oseq, Body: m.body, Replays: m.replays}, nil
}

// SendToDead 把队首消息移入死信区，返回新分配的死亡序号 dseq。
// 队列不存在或为空时拒绝，状态不变。
func (s *System) SendToDead(queue string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.queues[queue]
	if !ok {
		return 0, &RejectError{Reason: ReasonQueueNotFound, Queue: queue, Message: "send-to-dead: queue does not exist"}
	}
	if len(q) == 0 {
		return 0, &RejectError{Reason: ReasonNoDeadEntries, Queue: queue, Message: "send-to-dead: queue is empty"}
	}
	m := q[0]
	s.queues[queue] = q[1:]
	dseq := s.nextDSeq
	s.nextDSeq++
	s.dead = append(s.dead, &deadEntry{msg: m, queue: queue, dseq: dseq})
	return dseq, nil
}

// Replay 把死信区中属于 queue 且重放次数小于上限的条目，
// 按 oseq 升序追加回该队列队尾，至多 n 条；返回实际放回条数。
//
// 已达上限的条目被跳过、留在死信区且不占 n 的名额；
// 目标队列容量不足时已放回的不回滚，其余留在死信区。
// 整体拒绝的情形（优先级自上而下）：
//  1. n < 1 或队列名为空（ReasonInvalidArgument）
//  2. 队列不存在（ReasonQueueNotFound）
//  3. 死信区中没有该队列的条目（ReasonNoDeadEntries）
//  4. 有条目但全部已达重放上限（ReasonAllExhausted）
//  5. 目标队列已满致一条也放不进（ReasonQueueFull）
//
// 被拒绝时不改变队列、死信区与重放次数。
func (s *System) Replay(queue string, n int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n < 1 || queue == "" {
		return 0, &RejectError{Reason: ReasonInvalidArgument, Queue: queue, Message: "replay: n must be >= 1 and queue name non-empty"}
	}
	q, ok := s.queues[queue]
	if !ok {
		return 0, &RejectError{Reason: ReasonQueueNotFound, Queue: queue, Message: "replay: queue does not exist"}
	}

	var mine []*deadEntry
	for _, e := range s.dead {
		if e.queue == queue {
			mine = append(mine, e)
		}
	}
	if len(mine) == 0 {
		return 0, &RejectError{Reason: ReasonNoDeadEntries, Queue: queue, Message: "replay: no dead entries for queue"}
	}

	eligible := make([]*deadEntry, 0, len(mine))
	for _, e := range mine {
		if e.msg.replays < s.replayLimit {
			eligible = append(eligible, e)
		}
	}
	if len(eligible) == 0 {
		return 0, &RejectError{Reason: ReasonAllExhausted, Queue: queue, Message: "replay: all dead entries reached replay limit"}
	}
	// 按原入队序号 oseq 升序（而非死亡序）重放。
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].msg.oseq < eligible[j].msg.oseq })

	placed := 0
	consumed := make(map[*deadEntry]struct{})
	for _, e := range eligible {
		if placed >= n || len(q) >= s.capacity {
			break
		}
		e.msg.replays++
		q = append(q, e.msg)
		consumed[e] = struct{}{}
		placed++
	}
	if placed == 0 {
		return 0, &RejectError{Reason: ReasonQueueFull, Queue: queue, Message: "replay: target queue is full, no entry placed"}
	}

	s.queues[queue] = q
	kept := s.dead[:0]
	for _, e := range s.dead {
		if _, ok := consumed[e]; !ok {
			kept = append(kept, e)
		}
	}
	s.dead = kept
	return placed, nil
}

// QueueSnapshot 返回队列当前内容（队首在前）的副本；队列不存在时拒绝。
func (s *System) QueueSnapshot(queue string) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.queues[queue]
	if !ok {
		return nil, &RejectError{Reason: ReasonQueueNotFound, Queue: queue, Message: "snapshot: queue does not exist"}
	}
	out := make([]Message, 0, len(q))
	for _, m := range q {
		out = append(out, Message{OSeq: m.oseq, Body: m.body, Replays: m.replays})
	}
	return out, nil
}

// DeadSnapshot 返回死信区当前内容（按死亡序号升序）的副本。
func (s *System) DeadSnapshot() []DeadEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeadEntry, 0, len(s.dead))
	for _, e := range s.dead {
		out = append(out, DeadEntry{
			Message: Message{OSeq: e.msg.oseq, Body: e.msg.body, Replays: e.msg.replays},
			DSeq:    e.dseq,
		})
	}
	return out
}

// DeadQueues 返回死信区条目所属的队列名（与 DeadSnapshot 一一对应）。
func (s *System) DeadQueues() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.dead))
	for _, e := range s.dead {
		out = append(out, e.queue)
	}
	return out
}

// Capacity 返回每个命名队列的容量 C。
func (s *System) Capacity() int { return s.capacity }

// ReplayLimit 返回单条消息的重放次数上限 R。
func (s *System) ReplayLimit() int { return s.replayLimit }
