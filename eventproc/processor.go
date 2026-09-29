package eventproc

import (
	"fmt"
	"sort"
	"sync"
)

// batch 是内部批次：一批同时提交、按序 FIFO 的事件。
type batch struct {
	priority int
	events   []Event
	// seq 是该批次首个事件的全局到达序号，用于同优先级 FIFO 比较。
	seq int
	// cursor 是已处理位置（已处理事件个数），恢复时从 cursor 继续。
	cursor int
}

// Processor 是带优先级的抢占式变更事件处理器。
//
// 规则：
//   - 优先级按数值降序处理，同优先级按到达顺序先进先出（FIFO）。
//   - 一次 Advance 只推进一个事件（一个批次的一步）。
//   - 当更高优先级事件到达时，当前批次在当前事件边界被打断，
//     其已处理位置压栈保存，转而处理高优先级批次；
//     高优先级批次耗尽后，从栈顶恢复最近被打断的批次继续。
//   - 每次事件恰好处理一次，处理顺序只取决于提交序列，可复现。
type Processor struct {
	mu sync.RWMutex

	// waiting 按优先级保存尚未成为当前批次的批次队列，每个队列内 FIFO。
	waiting map[int][]*batch
	// current 为当前正在推进的批次；为 nil 表示处理器空闲。
	current *batch
	// stack 为被打断批次保存的现场，末尾为栈顶（最近一次保存）。
	// 结构保证：从栈底到栈顶优先级严格递增。
	stack []*batch
	// processed 为已处理事件 ID 序列。
	processed []string
	// live 记录当前仍占用的所有 ID（等待/当前/栈中/已处理），用于去重。
	live map[string]struct{}
	// arrivalSeq 为全局到达序号计数器，决定同优先级 FIFO。
	arrivalSeq int
}

// New 创建一个空处理器。
func New() *Processor {
	return &Processor{
		waiting: make(map[int][]*batch),
		live:    make(map[string]struct{}),
	}
}

// Enqueue 原子提交一个批次（同优先级按批次内顺序 FIFO）。
// 校验失败时整体拒绝，处理器状态不发生任何变化。
func (p *Processor) Enqueue(events []Event) error {
	if len(events) == 0 {
		return ErrEmptyBatch
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 整体校验：全部通过后才落任何变更，保证一次失败不改变任何状态。
	seen := make(map[string]struct{}, len(events))
	for _, ev := range events {
		if ev.Priority < 0 {
			return fmt.Errorf("%w: event %q has priority %d",
				ErrNegativePriority, ev.ID, ev.Priority)
		}
		if _, ok := seen[ev.ID]; ok {
			return fmt.Errorf("%w: event %q repeated within the same batch",
				ErrDuplicateID, ev.ID)
		}
		seen[ev.ID] = struct{}{}
		if _, ok := p.live[ev.ID]; ok {
			return fmt.Errorf("%w: event %q already exists",
				ErrDuplicateID, ev.ID)
		}
	}

	p.arrivalSeq++

	// 按事件优先级分组（同优先级的连续事件归入同一批次），
	// 同优先级内严格保持提交顺序（FIFO）。不同优先级即使交错提交，
	// 也各自排队，组内相对先后不变。
	groups := make([]*batch, 0, 1)
	for i := range events {
		ev := events[i]
		if n := len(groups); n > 0 && groups[n-1].priority == ev.Priority {
			groups[n-1].events = append(groups[n-1].events, ev)
		} else {
			groups = append(groups, &batch{
				priority: ev.Priority,
				events:   []Event{ev},
				seq:      p.arrivalSeq,
			})
		}
	}

	// 注册 ID。
	for _, ev := range events {
		p.live[ev.ID] = struct{}{}
	}

	// 提交批次的最高优先级：若严格高于当前批次，则在当前事件边界
	// 保存现场并抢占，最高优先级组立即成为当前批次。
	highest := groups[0]
	for _, g := range groups[1:] {
		if g.priority > highest.priority {
			highest = g
		}
	}

	for _, g := range groups {
		p.waiting[g.priority] = append(p.waiting[g.priority], g)
	}

	if p.current == nil {
		// 处理器空闲：调度逻辑会选到最高优先级组。
		p.scheduleLocked()
	} else if highest.priority > p.current.priority {
		// 抢占：当前批次现场压栈，最高优先级组出队成为当前批次。
		p.stack = append(p.stack, p.current)
		q := p.waiting[highest.priority]
		p.waiting[highest.priority] = q[1:]
		if len(q) == 1 {
			delete(p.waiting, highest.priority)
		}
		p.current = highest
	}
	return nil
}

// Advance 按抢占-恢复规则推进恰好一个事件，返回该事件 ID。
// 没有任何可处理事件时返回 ErrNoProcessable，且不改变任何状态。
func (p *Processor) Advance() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.scheduleLocked()
	if p.current == nil {
		return "", ErrNoProcessable
	}

	b := p.current
	id := b.events[b.cursor].ID
	b.cursor++
	p.processed = append(p.processed, id)

	// 当前批次恰好耗尽：下一次推进时再调度恢复。
	// 注意：已处理 ID 永久保留在 live 中，保证“全局唯一标识”
	// 对已处理事件同样成立（重复提交仍被拒绝）。
	if b.cursor == len(b.events) {
		p.current = nil
	}
	return id, nil
}

// scheduleLocked 选取下一个应推进的批次（调用方持锁）。
//
// 候选为：栈顶恢复帧与各优先级等待队列的队首批次。
// 选择优先级最高者；同优先级 FIFO，恢复帧总是早于仍在等待的批次，
// 因此栈顶帧优先。
func (p *Processor) scheduleLocked() {
	if p.current != nil {
		return
	}

	var best *batch

	if n := len(p.stack); n > 0 {
		best = p.stack[n-1]
	}

	var priorities []int
	for pr, q := range p.waiting {
		if len(q) > 0 {
			priorities = append(priorities, pr)
		}
	}
	sort.Ints(priorities)
	if len(priorities) > 0 {
		top := priorities[len(priorities)-1]
		cand := p.waiting[top][0]
		if best == nil || cand.priority > best.priority {
			best = cand
		}
	}

	if best == nil {
		return
	}

	p.current = best

	// 从其所在容器移除：栈顶帧弹栈，等待批次出队。
	if n := len(p.stack); n > 0 && p.stack[n-1] == best {
		p.stack = p.stack[:n-1]
		return
	}
	q := p.waiting[best.priority]
	p.waiting[best.priority] = q[1:]
	if len(q) == 1 {
		delete(p.waiting, best.priority)
	}
}
