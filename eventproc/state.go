package eventproc

import "fmt"

// Processed 返回已处理事件 ID 序列的深拷贝。
// 并发读安全：任意时刻读到的都是某个完整事件边界上的前缀。
func (p *Processor) Processed() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, len(p.processed))
	copy(out, p.processed)
	return out
}

// Snapshot 返回处理器完整状态的深拷贝，可用于日志打印与离线对照。
func (p *Processor) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()

	snap := Snapshot{
		Queued:    make(map[int][]BatchItem, len(p.waiting)),
		Stack:     make([]FrameView, 0, len(p.stack)),
		Processed: make([]string, len(p.processed)),
	}
	copy(snap.Processed, p.processed)

	for pr, q := range p.waiting {
		items := make([]BatchItem, 0)
		for _, b := range q {
			for i := b.cursor; i < len(b.events); i++ {
				items = append(items, toItem(b.events[i]))
			}
		}
		if len(items) > 0 {
			snap.Queued[pr] = items
		}
	}

	if p.current != nil {
		snap.Current = toBatchView(p.current)
	}

	for _, f := range p.stack {
		snap.Stack = append(snap.Stack, FrameView{
			Priority: f.priority,
			Cursor:   f.cursor,
		})
	}
	return snap
}

func toItem(ev Event) BatchItem {
	return BatchItem{ID: ev.ID, Priority: ev.Priority, Payload: ev.Payload}
}

func toBatchView(b *batch) *BatchView {
	events := make([]BatchItem, 0, len(b.events)-b.cursor)
	for i := b.cursor; i < len(b.events); i++ {
		events = append(events, toItem(b.events[i]))
	}
	return &BatchView{Priority: b.priority, Events: events, Cursor: b.cursor}
}

// SelfCheck 校验处理器内部结构性不变量。只读，不改变任何状态。
//
// 检查项：
//  1. 当前批次与栈帧的 cursor 均在合法区间内；
//  2. 栈从底到顶优先级严格递增（最近保存的现场优先级最高）；
//  3. 当前批次优先级严格高于所有栈帧与等待批次（否则说明有可抢占工作）；
//  4. 已处理序列中的 ID 全部唯一（每个事件恰好处理一次）；
//  5. 唯一性集合与 等待/当前/栈中/已处理 实际出现过的 ID 一一对应。
func (p *Processor) SelfCheck() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.current != nil {
		if err := checkCursor(p.current, "current"); err != nil {
			return err
		}
	}

	for i, f := range p.stack {
		if err := checkCursor(f, fmt.Sprintf("stack[%d]", i)); err != nil {
			return err
		}
		if i > 0 && f.priority <= p.stack[i-1].priority {
			return fmt.Errorf(
				"self-check: stack priority not strictly increasing at %d: %d <= %d",
				i, f.priority, p.stack[i-1].priority)
		}
	}

	if p.current != nil {
		for i, f := range p.stack {
			if f.priority >= p.current.priority {
				return fmt.Errorf(
					"self-check: stack[%d] priority %d >= current %d",
					i, f.priority, p.current.priority)
			}
		}
		for pr, q := range p.waiting {
			for _, b := range q {
				if b.priority >= p.current.priority {
					return fmt.Errorf(
						"self-check: waiting batch priority %d >= current %d",
						pr, p.current.priority)
				}
			}
		}
	}

	done := make(map[string]struct{}, len(p.processed))
	for _, id := range p.processed {
		if _, ok := done[id]; ok {
			return fmt.Errorf("self-check: processed id %q more than once", id)
		}
		done[id] = struct{}{}
	}

	occupied := make(map[string]struct{})
	for _, id := range p.processed {
		occupied[id] = struct{}{}
	}
	if p.current != nil {
		p.current.walkRemaining(func(ev Event) { occupied[ev.ID] = struct{}{} })
	}
	for _, q := range p.waiting {
		for _, b := range q {
			b.walkRemaining(func(ev Event) { occupied[ev.ID] = struct{}{} })
		}
	}
	for _, f := range p.stack {
		f.walkRemaining(func(ev Event) { occupied[ev.ID] = struct{}{} })
	}
	for id := range occupied {
		if _, ok := p.live[id]; !ok {
			return fmt.Errorf("self-check: id %q present but not in uniqueness set", id)
		}
	}
	for id := range p.live {
		if _, ok := occupied[id]; !ok {
			return fmt.Errorf("self-check: tracked id %q has no slot", id)
		}
	}
	return nil
}

func checkCursor(b *batch, where string) error {
	if b.cursor < 0 || b.cursor > len(b.events) {
		return fmt.Errorf("self-check: %s cursor %d out of range [0,%d]",
			where, b.cursor, len(b.events))
	}
	return nil
}

// walkRemaining 遍历批次中尚未处理的事件（调用方持锁）。
func (b *batch) walkRemaining(fn func(Event)) {
	for i := b.cursor; i < len(b.events); i++ {
		fn(b.events[i])
	}
}
