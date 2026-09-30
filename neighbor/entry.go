package neighbor

import "time"

// entry 为内部邻居条目骨架。
type entry struct {
	address   string
	linkLayer string
	state     State
	deadline  time.Time
	sent      int
	queue     []queuedPacket
}

type queuedPacket struct {
	packet any
}

// due 判断条目在 now 时刻是否到期（now 不早于到期时刻即到期）。
func (e *entry) due(now time.Time) bool {
	return !e.deadline.IsZero() && !now.Before(e.deadline)
}

// expire 对条目处理至多一个到期动作。
// emitRequest 在需要发送地址解析请求时被调用。
// 返回 deleted 表示条目应被删除，dropped 为随之计为不可达丢弃的暂存包数。
func (e *entry) expire(now time.Time, cfg Config, emitRequest func(address string)) (deleted bool, dropped int) {
	if !e.due(now) {
		return false, 0
	}
	switch e.state {
	case Reachable:
		// 可达到期：转陈旧，不再有到期时刻。
		e.state = Stale
		e.deadline = time.Time{}
	case Delay:
		// 延迟到期：转探测并发出第 1 次探测。
		e.state = Probe
		e.sent = 1
		e.deadline = now.Add(cfg.RetransTimer)
		emitRequest(e.address)
	case Incomplete, Probe:
		// 重发时刻到期：还能发则再发一次，否则删除条目。
		if e.sent < cfg.MaxAttempts {
			e.sent++
			e.deadline = now.Add(cfg.RetransTimer)
			emitRequest(e.address)
			return false, 0
		}
		dropped = len(e.queue)
		e.queue = nil
		return true, dropped
	}
	return false, 0
}

// enqueue 暂存一个包；超出上限 Q 时丢弃最旧的包，返回溢出丢弃数。
func (e *entry) enqueue(packet any, cfg Config) int {
	e.queue = append(e.queue, queuedPacket{packet: packet})
	dropped := 0
	for cfg.QueueLimit > 0 && len(e.queue) > cfg.QueueLimit {
		e.queue = e.queue[1:]
		dropped++
	}
	if cfg.QueueLimit <= 0 {
		dropped += len(e.queue)
		e.queue = nil
	}
	return dropped
}

// drainQueue 按入队顺序取出全部暂存包并清空队列。
func (e *entry) drainQueue() []any {
	if len(e.queue) == 0 {
		return nil
	}
	out := make([]any, len(e.queue))
	for i, qp := range e.queue {
		out[i] = qp.packet
	}
	e.queue = nil
	return out
}
