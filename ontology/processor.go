package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Event 是一个待处理的变更事件。
type Event struct {
	ID       string
	Priority int
}

// batch 记录某个优先级批次（FIFO 队列）已处理到的位置。
type batch struct {
	priority int
	position int
}

// opKind 标识操作日志条目类型。
type opKind int

const (
	opEnqueue opKind = iota
	opAdvance
)

// opEntry 是成功操作的确定性重放日志。
type opEntry struct {
	kind     opKind
	priority int
	ids      []string
	eventID  string
}

// Processor 是带优先级的抢占式变更事件处理器。
//
// 调度规则（每次 Advance 处理恰好一个事件）：
//   - 优先级数字越大越先处理；
//   - 同一优先级内部严格 FIFO；
//   - 当更高优先级事件到达时，当前批次在最近一个事件边界被打断，
//     其已处理位置压入保存现场栈，转去处理高优先级批次；
//   - 高优先级批次耗尽后，从栈顶弹出最近被打断的批次并从保存的
//     位置继续（LIFO 恢复）。
type Processor struct {
	mu        sync.RWMutex
	queues    map[int][]string // 每个优先级一条追加型 FIFO 队列
	pos       map[int]int      // 每条队列已处理到的位置
	current   *batch           // 当前正在推进的批次
	saved     []*batch         // 被打断批次的保存现场栈
	processed []Event          // 已处理序列（只能追加完整边界）
	seen      map[string]int   // 全局事件标识 -> 优先级
	log       []opEntry        // 成功操作日志，用于确定性重放自检
}

// New 创建一个空的处理器。
func New() *Processor {
	return &Processor{
		queues: make(map[int][]string),
		pos:    make(map[int]int),
		seen:   make(map[string]int),
	}
}

// Enqueue 原子地加入一批事件，任何一个非法则整批拒绝。
// 拒绝原因（整批、无副作用）：
//   - ErrNegativePriority：存在负优先级；
//   - ErrDuplicateID：批次内或与既有事件存在重复标识。
func (p *Processor) Enqueue(events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	// 先在锁外复制一份，避免调用方切片在校验期间被改动。
	incoming := make([]Event, len(events))
	copy(incoming, events)

	p.mu.Lock()
	defer p.mu.Unlock()

	// 只读预校验：任一条目不合法则整批拒绝，不触碰任何内部状态。
	local := make(map[string]struct{}, len(incoming))
	for _, e := range incoming {
		if e.Priority < 0 {
			return ErrNegativePriority
		}
		if _, dup := local[e.ID]; dup {
			return ErrDuplicateID
		}
		if _, dup := p.seen[e.ID]; dup {
			return ErrDuplicateID
		}
		local[e.ID] = struct{}{}
	}

	// 校验通过后按提交顺序落库，保证同优先级 FIFO。
	idsByPriority := make(map[int][]string)
	for _, e := range incoming {
		p.seen[e.ID] = e.Priority
		idsByPriority[e.Priority] = append(idsByPriority[e.Priority], e.ID)
	}
	order := make([]int, 0, len(idsByPriority))
	for priority := range idsByPriority {
		order = append(order, priority)
	}
	sort.Ints(order)
	for _, priority := range order {
		ids := idsByPriority[priority]
		p.queues[priority] = append(p.queues[priority], ids...)
		if _, ok := p.pos[priority]; !ok {
			p.pos[priority] = 0
		}
		p.log = append(p.log, opEntry{kind: opEnqueue, priority: priority, ids: ids})
	}
	return nil
}

// Advance 推进一个事件，无可处理事件时返回 ErrNoProcessable。
func (p *Processor) Advance() (Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	highest := -1
	for priority, queue := range p.queues {
		if p.pos[priority] < len(queue) && priority > highest {
			highest = priority
		}
	}
	if highest < 0 {
		return Event{}, ErrNoProcessable
	}

	// 更高优先级事件到达 -> 在当前事件边界打断当前批次并保存现场。
	if p.current != nil && p.current.priority != highest {
		if highest < p.current.priority {
			return Event{}, &InvariantError{Reason: "highest pending priority below current batch"}
		}
		p.saved = append(p.saved, p.current)
		p.current = nil
	}

	if p.current == nil {
		if p.current = popSaved(&p.saved, highest); p.current == nil {
			if p.pos[highest] != 0 {
				return Event{}, &InvariantError{Reason: "resumable batch missing from saved stack"}
			}
			p.current = &batch{priority: highest, position: 0}
		}
	}

	queue := p.queues[highest]
	position := p.pos[highest]
	if position >= len(queue) {
		return Event{}, &InvariantError{Reason: "selected queue already exhausted"}
	}
	id := queue[position]
	position++
	p.pos[highest] = position
	p.current.position = position

	if position >= len(queue) {
		// 当前批次恰好耗尽：退役现场并裁剪只增队列。
		delete(p.queues, highest)
		delete(p.pos, highest)
		p.current = nil
	}

	e := Event{ID: id, Priority: highest}
	p.processed = append(p.processed, e)
	p.log = append(p.log, opEntry{kind: opAdvance, priority: highest, eventID: id})
	return e, nil
}

// Processed 返回已处理序列的完整副本。
func (p *Processor) Processed() []Event {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Event(nil), p.processed...)
}

// Snapshot 返回调试用全量状态快照。
func (p *Processor) Snapshot() StateSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	pending := make(map[int][]string, len(p.queues))
	for priority, queue := range p.queues {
		pending[priority] = append([]string(nil), queue[p.pos[priority]:]...)
	}
	snap := StateSnapshot{
		Pending:   pending,
		Processed: append([]Event(nil), p.processed...),
	}
	if p.current != nil {
		snap.Current = p.current.snapshot(p.queues)
	}
	for _, b := range p.saved {
		snap.Saved = append(snap.Saved, *b.snapshot(p.queues))
	}
	return snap
}

// Check 执行内部不变量自检。
func (p *Processor) Check() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.queues) != len(p.pos) {
		return &InvariantError{Reason: "queue/position table size mismatch"}
	}

	// 1) 已处理事件标识全局唯一，且优先级登记一致。
	dedup := make(map[string]int, len(p.processed))
	for _, e := range p.processed {
		if _, ok := dedup[e.ID]; ok {
			return &InvariantError{Reason: fmt.Sprintf("processed id %q appears more than once", e.ID)}
		}
		dedup[e.ID] = e.Priority
		if prio, ok := p.seen[e.ID]; !ok || prio != e.Priority {
			return &InvariantError{Reason: fmt.Sprintf("processed id %q priority registry mismatch", e.ID)}
		}
	}

	// 2) 保存现场栈严格按优先级降序（栈顶最近被打断、优先级最低）。
	for i := 1; i < len(p.saved); i++ {
		if p.saved[i-1].priority >= p.saved[i].priority {
			return &InvariantError{Reason: "saved stack not strictly ascending in priority (bottom to top)"}
		}
	}

	// 3) 位置表合法；现场位置与位置表一致。
	for priority, queue := range p.queues {
		position, ok := p.pos[priority]
		if !ok || position < 0 || position > len(queue) {
			return &InvariantError{Reason: fmt.Sprintf("illegal position for priority %d", priority)}
		}
		if position == len(queue) {
			return &InvariantError{Reason: fmt.Sprintf("exhausted queue %d not retired", priority)}
		}
	}
	if p.current != nil {
		if p.current.position != p.pos[p.current.priority] {
			return &InvariantError{Reason: "current batch position out of sync"}
		}
	}
	for _, b := range p.saved {
		if b.position != p.pos[b.priority] {
			return &InvariantError{Reason: "saved batch position out of sync"}
		}
	}

	// 4) 任何已登记的优先级都必须存在于队列映射中，反之亦然。
	seenPriorities := make(map[int]struct{})
	for _, priority := range p.seen {
		seenPriorities[priority] = struct{}{}
	}
	for priority := range p.queues {
		if _, ok := seenPriorities[priority]; !ok {
			return &InvariantError{Reason: fmt.Sprintf("queue priority %d missing from registry", priority)}
		}
	}

	// 5) 用操作日志重放到一个全新实例，结果必须逐元素一致。
	replay := New()
	for _, entry := range p.log {
		switch entry.kind {
		case opEnqueue:
			batch := make([]Event, 0, len(entry.ids))
			for _, id := range entry.ids {
				batch = append(batch, Event{ID: id, Priority: entry.priority})
			}
			if err := replay.Enqueue(batch...); err != nil {
				return &InvariantError{Reason: "replay enqueue failed: " + err.Error()}
			}
		case opAdvance:
			e, err := replay.Advance()
			if err != nil {
				return &InvariantError{Reason: "replay advance failed: " + err.Error()}
			}
			if e.ID != entry.eventID || e.Priority != entry.priority {
				return &InvariantError{Reason: "replay event mismatch"}
			}
		}
	}
	if len(replay.processed) != len(p.processed) {
		return &InvariantError{Reason: "replay length mismatch"}
	}
	for i := range p.processed {
		if replay.processed[i] != p.processed[i] {
			return &InvariantError{Reason: fmt.Sprintf("replay mismatch at index %d", i)}
		}
	}
	return nil
}

// popSaved 从保存现场栈中找出并弹出指定优先级的现场。
// 正常调度下它总在栈顶；搜索移除是为了在任何情形下都保持数据结构自洽。
func popSaved(saved *[]*batch, priority int) *batch {
	for i := len(*saved) - 1; i >= 0; i-- {
		if (*saved)[i].priority == priority {
			b := (*saved)[i]
			*saved = append((*saved)[:i], (*saved)[i+1:]...)
			return b
		}
	}
	return nil
}

func (b *batch) snapshot(queues map[int][]string) *BatchSnapshot {
	snap := &BatchSnapshot{Priority: b.priority, Position: b.position}
	if queue := queues[b.priority]; b.position < len(queue) {
		snap.Next = queue[b.position]
	}
	return snap
}

// StateSnapshot 是处理器某一时刻的状态快照，供日志与测试使用。
type StateSnapshot struct {
	Pending   map[int][]string
	Current   *BatchSnapshot
	Saved     []BatchSnapshot
	Processed []Event
}

// BatchSnapshot 是一个批次（某优先级 FIFO 队列）的处理位置快照。
type BatchSnapshot struct {
	Priority int
	Position int
	Next     string
}
