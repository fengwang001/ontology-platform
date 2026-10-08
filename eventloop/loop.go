package eventloop

import (
	"container/heap"
	"fmt"
	"sync"
)

// Stats 是可验证的性能计数器：
// 每次任务选择检查的队首数（HeadChecks）不超过 2*源数，
// 与各源队列中待执行任务总数无关；每次微任务检查点
// （Checkpoints）只访问微任务队列，不触碰任务队列。
type Stats struct {
	Selections  int64
	HeadChecks  int64
	Checkpoints int64
}

// AdvanceResult 是单次推进的日志：输入区间、输出轨迹与期间异常报告。
type AdvanceResult struct {
	From    int64
	To      int64
	Entries []TraceEntry
	Errors  []ErrorReport
}

type handleKind int

const (
	kindTaskSource handleKind = iota
	kindMicrotask
	kindTimer
	kindAnimationFrame
	kindIdle
)

type registrant struct {
	kind   handleKind
	cancel func()
}

type microtask struct {
	handle    Handle
	cb        func()
	cancelled bool
}

type rafCallback struct {
	handle    Handle
	cb        func()
	cancelled bool
}

type idleCallback struct {
	handle    Handle
	cb        func(IdleDeadline)
	cancelled bool
	executed  bool
}

// EventLoop 是任务调度内核。入队、取消与标脏可由多个线程并发调用
// （内部以 mu 串行化，等价于某个串行顺序）；AdvanceTo 之间以 execMu 串行。
type EventLoop struct {
	mu     sync.Mutex
	execMu sync.Mutex

	cfg    Config
	now    int64
	target int64
	seq    uint64
	next   Handle

	sched scheduler

	micro []*microtask

	timers timerHeap

	rafPending           []*rafCallback
	dirty                bool
	lastRenderedBoundary int64

	idlePending  []*idleCallback
	idleTimeouts idleTimeoutHeap
	idleCooldown bool

	curTimerDepth int

	registry     map[Handle]registrant
	cancelledSet map[Handle]bool

	trace      []TraceEntry
	errReports []ErrorReport
	pending    *AdvanceResult
	stats      Stats
}

// NewEventLoop 校验配置并创建内核。
func NewEventLoop(cfg Config) (*EventLoop, error) {
	if cfg.FrameInterval <= 0 {
		return nil, &Error{Category: ErrInvalidArg, Message: "frame interval must be positive"}
	}
	if cfg.StarvationLimit < 0 {
		return nil, &Error{Category: ErrInvalidArg, Message: "starvation limit must be non-negative"}
	}
	if cfg.MinTimerDelay < 0 || cfg.TimerClampDelay < 0 || cfg.TimerNestingThreshold < 0 {
		return nil, &Error{Category: ErrInvalidArg, Message: "timer delays and nesting threshold must be non-negative"}
	}
	l := &EventLoop{
		cfg:           cfg,
		curTimerDepth: -1,
		registry:      make(map[Handle]registrant),
		cancelledSet:  make(map[Handle]bool),
	}
	l.sched = scheduler{limit: cfg.StarvationLimit, stats: &l.stats}
	return l, nil
}

// Now 返回当前注入时钟时刻。
func (l *EventLoop) Now() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.now
}

// Stats 返回性能计数器快照。
func (l *EventLoop) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats
}

// Trace 返回完整执行轨迹。
func (l *EventLoop) Trace() []TraceEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]TraceEntry(nil), l.trace...)
}

// Errors 返回全部异常报告，次序等于抛出次序。
func (l *EventLoop) Errors() []ErrorReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ErrorReport(nil), l.errReports...)
}

// ErrorsBetween 按时刻区间 [from, to] 查询异常报告。
func (l *EventLoop) ErrorsBetween(from, to int64) []ErrorReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []ErrorReport
	for _, r := range l.errReports {
		if r.Time >= from && r.Time <= to {
			out = append(out, r)
		}
	}
	return out
}

func (l *EventLoop) allocHandleLocked() Handle {
	l.next++
	return l.next
}

// EnqueueTask 以当前时刻为入队时刻，把任务放入指定源队列尾部。
func (l *EventLoop) EnqueueTask(src Source, cb func()) (Handle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !src.valid() {
		return 0, &Error{Category: ErrInvalidArg, Message: fmt.Sprintf("unknown task source %d", int(src))}
	}
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil task callback"}
	}
	h := l.allocHandleLocked()
	t := &task{handle: h, source: src, enqueueTime: l.now, cb: cb}
	l.sched.enqueue(t)
	l.registry[h] = registrant{kind: kindTaskSource, cancel: func() { t.cancelled = true }}
	return h, nil
}

// QueueMicrotask 注册一个微任务，在下一个微任务检查点执行。
func (l *EventLoop) QueueMicrotask(cb func()) (Handle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil microtask callback"}
	}
	h := l.allocHandleLocked()
	m := &microtask{handle: h, cb: cb}
	l.micro = append(l.micro, m)
	l.registry[h] = registrant{kind: kindMicrotask, cancel: func() { m.cancelled = true }}
	return h, nil
}

// RequestAnimationFrame 注册动画帧回调，构成一次渲染请求。
func (l *EventLoop) RequestAnimationFrame(cb func()) (Handle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil animation frame callback"}
	}
	h := l.allocHandleLocked()
	r := &rafCallback{handle: h, cb: cb}
	l.rafPending = append(l.rafPending, r)
	l.registry[h] = registrant{kind: kindAnimationFrame, cancel: func() { r.cancelled = true }}
	return h, nil
}

// MarkDirty 把文档标为脏，构成一次渲染请求。
func (l *EventLoop) MarkDirty() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dirty = true
}

// RequestIdleCallback 注册空闲回调；timeout 为 NoTimeout 表示无超时。
// 带超时的回调在超时时刻到达仍未执行时，作为内部源任务按超时时刻入队。
func (l *EventLoop) RequestIdleCallback(cb func(IdleDeadline), timeout int64) (Handle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil idle callback"}
	}
	if timeout < 0 {
		return 0, &Error{Category: ErrInvalidArg, Message: "negative idle timeout"}
	}
	h := l.allocHandleLocked()
	ic := &idleCallback{handle: h, cb: cb}
	l.idlePending = append(l.idlePending, ic)
	if timeout != NoTimeout {
		l.seq++
		heap.Push(&l.idleTimeouts, idleTimeout{deadline: l.now + timeout, seq: l.seq, cb: ic})
	}
	l.registry[h] = registrant{kind: kindIdle, cancel: func() { ic.cancelled = true }}
	return h, nil
}

// SetTimeout 注册定时器。延迟小于最小值按最小值处理；
// 嵌套深度超过阈值时延迟不足钳制值的按钳制值处理（等于阈值不钳制）。
func (l *EventLoop) SetTimeout(cb func(), delay int64) (Handle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cb == nil {
		return 0, &Error{Category: ErrInvalidArg, Message: "nil timer callback"}
	}
	if delay < 0 {
		return 0, &Error{Category: ErrInvalidArg, Message: "negative timer delay"}
	}
	depth := 0
	if l.curTimerDepth >= 0 {
		depth = l.curTimerDepth + 1
	}
	eff := delay
	if depth > l.cfg.TimerNestingThreshold && eff < l.cfg.TimerClampDelay {
		eff = l.cfg.TimerClampDelay
	}
	if eff < l.cfg.MinTimerDelay {
		eff = l.cfg.MinTimerDelay
	}
	h := l.allocHandleLocked()
	l.seq++
	tm := &timer{handle: h, due: l.now + eff, seq: l.seq, depth: depth, cb: cb}
	heap.Push(&l.timers, tm)
	l.registry[h] = registrant{kind: kindTimer, cancel: func() {
		tm.cancelled = true
		if tm.task != nil {
			tm.task.cancelled = true
		}
	}}
	return h, nil
}

// Cancel 取消一个尚未执行的句柄。已入队但未执行的定时器任务被取消后绝不执行。
func (l *EventLoop) Cancel(h Handle) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancelledSet[h] {
		return &Error{Category: ErrDuplicateCancel, Message: fmt.Sprintf("handle %d already cancelled", h)}
	}
	r, ok := l.registry[h]
	if !ok {
		return &Error{Category: ErrHandleNotFound, Message: fmt.Sprintf("handle %d not found", h)}
	}
	r.cancel()
	delete(l.registry, h)
	l.cancelledSet[h] = true
	return nil
}

// AdvanceTo 把注入时钟推进到 target，返回本次推进的日志。
// 推进过程中发生的全部执行次序唯一确定；target 小于当前时刻报时钟回退，
// 被拒绝的推进不改变任何队列、标记与时钟。
func (l *EventLoop) AdvanceTo(target int64) (AdvanceResult, error) {
	l.execMu.Lock()
	defer l.execMu.Unlock()
	l.mu.Lock()
	if target < l.now {
		cur := l.now
		l.mu.Unlock()
		return AdvanceResult{}, &Error{
			Category: ErrClockRollback,
			Message:  fmt.Sprintf("clock rollback: cannot advance from %d to %d", cur, target),
		}
	}
	res := &AdvanceResult{From: l.now, To: target}
	l.pending = res
	l.target = target
	l.mu.Unlock()

	for {
		step := l.planNext()
		if step == nil {
			break
		}
		step()
	}

	l.mu.Lock()
	l.pending = nil
	l.mu.Unlock()
	return *res, nil
}

// planNext 在锁内决定下一原子动作，返回的动作在锁外执行，
// 因此回调可以安全地重入公开 API。返回 nil 表示推进结束。
// 决策次序：微任务检查点 → 任务选择 → 触发到期事件 → 渲染 → 空闲 → 推进时钟。
func (l *EventLoop) planNext() func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		l.sched.discardCancelledHeads()

		// 微任务检查点：任务、渲染或空闲回调产生的微任务必须先排空。
		if len(l.micro) > 0 {
			return l.drainMicrotasks
		}

		// 任务选择（含饥饿强制与用户交互优先）。
		if t, reason := l.sched.selectNext(); t != nil {
			delete(l.registry, t.handle)
			task := t
			selReason := reason
			if task.note != "" {
				selReason = task.note + " | " + reason
			}
			return func() {
				l.executeCallback(ExecTask, task.source, task.handle, selReason, task.cb)
				l.drainMicrotasks()
			}
		}

		// 触发到期时刻不超过当前时刻的定时器与空闲超时。
		if l.fireDueEventsLocked(l.now) {
			continue
		}

		// 渲染机会：当前时刻已越过某个帧边界。
		// 同一帧边界至多渲染一次；无渲染请求时跳过该边界且不计为错过。
		boundary := (l.now / l.cfg.FrameInterval) * l.cfg.FrameInterval
		if boundary > l.lastRenderedBoundary {
			l.lastRenderedBoundary = boundary
			if l.renderRequestedLocked() {
				snap := l.rafPending
				l.rafPending = nil
				return func() { l.runRender(snap, boundary) }
			}
			continue
		}

		// 空闲期：无任务、无微任务、无渲染请求、剩余时间严格大于零。
		if l.idleEligibleLocked() {
			snap := l.idlePending
			l.idlePending = nil
			l.idleCooldown = true
			return func() { l.runIdlePeriod(snap) }
		}

		// 无事可做：推进时钟到下一个事件时刻或目标时刻。
		next, ok := l.nextEventLocked()
		if ok && next <= l.target {
			l.now = next
			l.idleCooldown = false
			l.fireDueEventsLocked(l.now)
			continue
		}
		if l.now < l.target {
			l.now = l.target
			l.idleCooldown = false
			continue
		}
		return nil
	}
}

// executeCallback 记录轨迹并执行回调；回调抛出的异常（panic）进入
// 错误报告，不中断其后的微任务与任务。仅在 AdvanceTo 协程上调用。
func (l *EventLoop) executeCallback(typ ExecType, src Source, h Handle, reason string, cb func()) {
	l.mu.Lock()
	entry := TraceEntry{Type: typ, Source: src, Handle: h, Time: l.now, Reason: reason}
	l.trace = append(l.trace, entry)
	if l.pending != nil {
		l.pending.Entries = append(l.pending.Entries, entry)
	}
	l.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			l.mu.Lock()
			rep := ErrorReport{Time: l.now, Handle: h, Value: r}
			l.errReports = append(l.errReports, rep)
			if l.pending != nil {
				l.pending.Errors = append(l.pending.Errors, rep)
			}
			l.mu.Unlock()
		}
	}()
	cb()
}

// drainMicrotasks 是一次微任务检查点：排空整个微任务队列，
// 包括执行期间新产生的微任务，无论嵌套多深。
// 只访问微任务队列，开销与任务队列规模无关。
func (l *EventLoop) drainMicrotasks() {
	l.mu.Lock()
	l.stats.Checkpoints++
	l.mu.Unlock()
	for {
		l.mu.Lock()
		if len(l.micro) == 0 {
			l.mu.Unlock()
			return
		}
		m := l.micro[0]
		l.micro = l.micro[1:]
		delete(l.registry, m.handle)
		l.mu.Unlock()
		if m.cancelled {
			continue
		}
		l.executeCallback(ExecMicrotask, NoSource, m.handle, "microtask checkpoint", m.cb)
	}
}

// runRender 执行一次渲染：按注册次序执行快照中的所有动画帧回调
// （其间注册的回调推迟到下一次渲染），每个回调之后都有微任务检查点，
// 最后清除脏标记。
func (l *EventLoop) runRender(snap []*rafCallback, boundary int64) {
	reason := fmt.Sprintf("frame boundary %d crossed", boundary)
	for _, r := range snap {
		l.mu.Lock()
		if r.cancelled {
			l.mu.Unlock()
			continue
		}
		delete(l.registry, r.handle)
		l.mu.Unlock()
		l.executeCallback(ExecAnimationFrame, NoSource, r.handle, reason, r.cb)
		l.drainMicrotasks()
	}
	l.mu.Lock()
	l.dirty = false
	l.mu.Unlock()
}

// idleRemainingLocked 是距下一帧边界的剩余时间；时刻恰在帧边界上时为零。
func (l *EventLoop) idleRemainingLocked() int64 {
	f := l.cfg.FrameInterval
	if l.now%f == 0 {
		return 0
	}
	return f - l.now%f
}

func (l *EventLoop) renderRequestedLocked() bool {
	if l.dirty {
		return true
	}
	for _, r := range l.rafPending {
		if !r.cancelled {
			return true
		}
	}
	return false
}

func (l *EventLoop) idleEligibleLocked() bool {
	if l.idleCooldown {
		return false
	}
	live := false
	for _, ic := range l.idlePending {
		if !ic.cancelled && !ic.executed {
			live = true
			break
		}
	}
	if !live {
		return false
	}
	if l.sched.hasRunnable() || len(l.micro) > 0 || l.renderRequestedLocked() {
		return false
	}
	return l.idleRemainingLocked() > 0
}

// runIdlePeriod 执行一个空闲期：只执行期开始前已注册的快照回调，
// 期内注册的新回调推迟到下一空闲期；每个回调前重新检查空闲条件，
// 每个回调之后都有微任务检查点。
func (l *EventLoop) runIdlePeriod(snap []*idleCallback) {
	for i, ic := range snap {
		l.mu.Lock()
		if ic.cancelled || ic.executed {
			l.mu.Unlock()
			continue
		}
		l.sched.discardCancelledHeads()
		ok := !l.sched.hasRunnable() && len(l.micro) == 0 &&
			!l.renderRequestedLocked() && l.idleRemainingLocked() > 0
		if !ok {
			rest := append([]*idleCallback(nil), snap[i:]...)
			l.idlePending = append(rest, l.idlePending...)
			l.mu.Unlock()
			return
		}
		ic.executed = true
		delete(l.registry, ic.handle)
		remaining := l.idleRemainingLocked()
		deadline := l.now + remaining
		l.mu.Unlock()
		l.executeCallback(ExecIdle, NoSource, ic.handle, "idle period", func() {
			ic.cb(IdleDeadline{Remaining: remaining, Deadline: deadline})
		})
		l.drainMicrotasks()
	}
}

// fireDueEventsLocked 触发到期时刻不超过 now 的定时器与空闲超时，
// 把它们变成任务入队（入队时刻取到期时刻）。返回是否有事件被触发。
func (l *EventLoop) fireDueEventsLocked(now int64) bool {
	fired := false
	for len(l.timers) > 0 && l.timers[0].due <= now {
		tm := heap.Pop(&l.timers).(*timer)
		if tm.cancelled {
			continue
		}
		fired = true
		h := l.allocHandleLocked()
		depth, cb, tmHandle := tm.depth, tm.cb, tm.handle
		t := &task{
			handle:      h,
			source:      SourceTimer,
			enqueueTime: tm.due,
			note:        fmt.Sprintf("timer %d due", tmHandle),
			cb: func() {
				l.mu.Lock()
				delete(l.registry, tmHandle)
				prev := l.curTimerDepth
				l.curTimerDepth = depth
				l.mu.Unlock()
				defer func() {
					l.mu.Lock()
					l.curTimerDepth = prev
					l.mu.Unlock()
				}()
				cb()
			},
		}
		tm.task = t
		l.sched.enqueue(t)
		l.registry[h] = registrant{kind: kindTaskSource, cancel: func() { t.cancelled = true }}
	}
	for len(l.idleTimeouts) > 0 && l.idleTimeouts[0].deadline <= now {
		it := heap.Pop(&l.idleTimeouts).(idleTimeout)
		ic := it.cb
		if ic.cancelled || ic.executed {
			continue
		}
		fired = true
		ic.executed = true
		h := l.allocHandleLocked()
		t := &task{
			handle:      h,
			source:      SourceInternal,
			enqueueTime: it.deadline,
			note:        fmt.Sprintf("idle callback %d timed out", ic.handle),
			cb: func() {
				l.mu.Lock()
				cancelled := ic.cancelled
				delete(l.registry, ic.handle)
				remaining := l.idleRemainingLocked()
				deadline := l.now + remaining
				l.mu.Unlock()
				if cancelled {
					return
				}
				ic.cb(IdleDeadline{Remaining: remaining, Deadline: deadline, DidTimeout: true})
			},
		}
		l.sched.enqueue(t)
		l.registry[h] = registrant{kind: kindTaskSource, cancel: func() { t.cancelled = true }}
	}
	return fired
}

// nextEventLocked 返回下一个可使时钟推进的事件时刻（最早的定时器
// 到期或空闲超时），并顺带清理堆顶的失效条目。
func (l *EventLoop) nextEventLocked() (int64, bool) {
	for len(l.timers) > 0 && l.timers[0].cancelled {
		heap.Pop(&l.timers)
	}
	for len(l.idleTimeouts) > 0 &&
		(l.idleTimeouts[0].cb.cancelled || l.idleTimeouts[0].cb.executed) {
		heap.Pop(&l.idleTimeouts)
	}
	var best int64
	ok := false
	if len(l.timers) > 0 {
		best, ok = l.timers[0].due, true
	}
	if len(l.idleTimeouts) > 0 && (!ok || l.idleTimeouts[0].deadline < best) {
		best, ok = l.idleTimeouts[0].deadline, true
	}
	return best, ok
}
