package ontology

import (
	"container/heap"
	"fmt"
	"runtime/debug"
)

func (loop *Loop) EnqueueTask(source Source, fn TaskFn) (Handle, error) {
	if fn == nil {
		return 0, invalidArgument("callback must not be nil")
	}
	if !validSource(source) {
		return 0, invalidArgument("unknown task source")
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	handle := loop.allocLocked()
	loop.pushTaskLocked(&task{
		handle: handle,
		source: source,
		enqAt:  loop.now,
		seq:    loop.nextSequenceLocked(),
		fn:     fn,
	})
	return handle, nil
}

func (loop *Loop) EnqueueMicrotask(fn MicrotaskFn) (Handle, error) {
	return loop.enqueueMicrotask(fn, 0)
}

func (loop *Loop) enqueueMicrotask(fn MicrotaskFn, depth int) (Handle, error) {
	if fn == nil {
		return 0, invalidArgument("callback must not be nil")
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	handle := loop.allocLocked()
	item := &microtask{handle: handle, depth: depth, fn: fn}
	element := loop.microtasks.PushBack(item)
	loop.microByID[handle] = element
	return handle, nil
}

func (loop *Loop) RequestFrame(fn FrameFn) (Handle, error) {
	if fn == nil {
		return 0, invalidArgument("callback must not be nil")
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	handle := loop.allocLocked()
	item := &frameCallback{handle: handle, bornEpoch: loop.idleEpoch, fn: fn}
	element := loop.frames.PushBack(item)
	loop.frameByID[handle] = element
	return handle, nil
}

func (loop *Loop) RequestIdle(timeout Time, fn IdleFn) (Handle, error) {
	if fn == nil {
		return 0, invalidArgument("callback must not be nil")
	}
	if timeout < 0 {
		return 0, invalidArgument("idle timeout must not be negative")
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	handle := loop.allocLocked()
	item := &idleCallback{handle: handle, timeout: timeout, dueAt: loop.now + timeout, hasTO: timeout > 0, bornEpoch: loop.idleEpoch, fn: fn}
	element := loop.idles.PushBack(item)
	loop.idleByID[handle] = element
	return handle, nil
}

func (loop *Loop) SetTimeout(delay Time, fn TaskFn) (Handle, error) {
	return loop.setTimeout(delay, fn, 0)
}

func (loop *Loop) setTimeout(delay Time, fn TaskFn, depth int) (Handle, error) {
	if delay < 0 {
		return 0, invalidArgument("timer delay must not be negative")
	}
	if fn == nil {
		return 0, invalidArgument("callback must not be nil")
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	effective := delay
	if effective < loop.config.MinTimerDelay {
		effective = loop.config.MinTimerDelay
	}
	if depth > loop.config.NestedTimerThreshold && effective < loop.config.ClampedTimerDelay {
		effective = loop.config.ClampedTimerDelay
	}
	handle := loop.allocLocked()
	loop.pushTimerHeapLocked(&timerEntry{
		handle: handle,
		due:    loop.now + effective,
		seq:    loop.nextSequenceLocked(),
		depth:  depth + 1,
		fn:     fn,
	})
	return handle, nil
}

func (loop *Loop) Cancel(handle Handle) error {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	state, ok := loop.state[handle]
	if !ok || state == stateDone {
		return ErrUnknownHandle
	}
	if state == stateCanceled {
		return ErrDuplicateCancel
	}
	loop.state[handle] = stateCanceled
	if element, ok := loop.microByID[handle]; ok {
		loop.microtasks.Remove(element)
		delete(loop.microByID, handle)
	}
	if element, ok := loop.frameByID[handle]; ok {
		loop.frames.Remove(element)
		delete(loop.frameByID, handle)
	}
	if element, ok := loop.idleByID[handle]; ok {
		loop.idles.Remove(element)
		delete(loop.idleByID, handle)
	}
	if timer, ok := loop.timerByID[handle]; ok {
		for index, candidate := range loop.timers {
			if candidate == timer {
				heap.Remove(&loop.timers, index)
				break
			}
		}
		delete(loop.timerByID, handle)
	}
	if element, ok := loop.taskByID[handle]; ok {
		item := element.Value.(*task)
		loop.queues[item.source].tasks.Remove(element)
		delete(loop.taskByID, handle)
		loop.cleanHead(item.source)
	}
	return nil
}

func (loop *Loop) MarkDirty() {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	loop.dirty = true
}

func (loop *Loop) Advance(target Time) ([]TraceEvent, error) {
	if target < 0 {
		return nil, invalidArgument("target time must not be negative")
	}
	loop.mu.Lock()
	if target < loop.now {
		current := loop.now
		loop.mu.Unlock()
		return nil, fmt.Errorf("%w: %d is before %d", ErrClockRollback, target, current)
	}
	loop.mu.Unlock()

	loop.execMu.Lock()
	defer loop.execMu.Unlock()

	loop.mu.Lock()
	start := loop.now
	traceStart := len(loop.traces)
	loop.logger.Logf("advance input: now=%d target=%d runnable=%d microtasks=%d frames=%d dirty=%v",
		start, target, loop.runnableCountLocked(), loop.microtasks.Len(), loop.frames.Len(), loop.dirty)
	loop.mu.Unlock()

	for {
		loop.mu.Lock()
		loop.advanceFrameLocked(false, target)
		loop.materializeTimersLocked(target)

		if chosen := loop.selectTaskLocked(); chosen != nil {
			loop.recordSkipsLocked(chosen)
			loop.removeTaskLocked(chosen)
			now := loop.now
			executionDepth := chosen.depth
			if chosen.source == SourceTimer {
				executionDepth = chosen.depth
			}
			ctx := &Context{loop: loop, depth: executionDepth, handle: chosen.handle}
			fn := chosen.fn
			eventType := EventTask
			traceHandle := chosen.handle
			if chosen.source == SourceTimer {
				eventType = EventTimer
			} else if chosen.idle != nil {
				eventType = EventIdleTiming
				traceHandle = chosen.idleHandle
			}
			loop.appendTraceLocked(TraceEvent{Type: eventType, Handle: traceHandle, Time: now, Source: chosen.source, Depth: chosen.depth})
			loop.mu.Unlock()
			recovered := loop.invokeTask(fn, ctx)
			loop.mu.Lock()
			if recovered != nil {
				loop.reportPanicLocked(traceHandle, recovered)
			}
			loop.runMicrotasksLocked()
			if loop.now >= loop.nextFrame && loop.renderRequestedLocked() {
				frameTime := missedBoundary(loop.now, loop.config.FrameInterval)
				loop.materializeTimersLocked(frameTime)
				if !loop.hasTaskUntilLocked(frameTime) {
					loop.now = frameTime
					loop.nextFrame = nextBoundary(frameTime, loop.config.FrameInterval)
					loop.mu.Unlock()
					loop.render(frameTime)
					continue
				}
			}
			loop.mu.Unlock()
			continue
		}

		if loop.microtasks.Len() > 0 {
			loop.runMicrotasksLocked()
			loop.mu.Unlock()
			continue
		}

		if loop.now >= loop.nextFrame && loop.renderRequestedLocked() {
			frameTime := missedBoundary(loop.now, loop.config.FrameInterval)
			loop.materializeTimersLocked(frameTime)
			if loop.hasTaskUntilLocked(frameTime) {
				loop.mu.Unlock()
				continue
			}
			loop.now = frameTime
			loop.nextFrame = nextBoundary(frameTime, loop.config.FrameInterval)
			loop.mu.Unlock()
			loop.render(frameTime)
			continue
		}

		if loop.canIdleLocked(target) {
			loop.runIdlePeriodLocked(target)
			loop.mu.Unlock()
			continue
		}

		if loop.now < target {
			next := target
			if loop.timers.Len() > 0 && loop.timers[0].due > loop.now && loop.timers[0].due < next {
				next = loop.timers[0].due
			}
			for element := loop.idles.Front(); element != nil; element = element.Next() {
				item := element.Value.(*idleCallback)
				if item.hasTO && item.dueAt > loop.now && item.dueAt < next {
					next = item.dueAt
				}
			}
			loop.now = next
			loop.logger.Logf("advance clock: now=%d target=%d", next, target)
			loop.mu.Unlock()
			continue
		}

		events := append([]TraceEvent(nil), loop.traces[traceStart:]...)
		loop.logger.Logf("advance output: start=%d end=%d events=%d decision=settled", start, loop.now, len(events))
		loop.mu.Unlock()
		return events, nil
	}
}

func (loop *Loop) Errors(from, to Time) []ErrorReport {
	if from < 0 || to < 0 || from > to {
		return nil
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	var result []ErrorReport
	for _, report := range loop.errors {
		if report.Time >= from && report.Time <= to {
			result = append(result, report)
		}
	}
	return result
}

func (loop *Loop) removeTaskLocked(item *task) {
	element, ok := loop.taskByID[item.handle]
	if ok {
		loop.queues[item.source].tasks.Remove(element)
		delete(loop.taskByID, item.handle)
	}
	loop.state[item.handle] = stateDone
	loop.cleanHead(item.source)
}

func (loop *Loop) runnableCountLocked() int {
	total := 0
	for source := SourceUser; source < sourceCount; source++ {
		loop.cleanHead(source)
		if loop.queues[source].tasks.Len() > 0 && loop.queues[source].tasks.Front().Value.(*task).enqAt <= loop.now {
			total += loop.queues[source].tasks.Len()
		}
	}
	return total
}

func (loop *Loop) appendTraceLocked(event TraceEvent) {
	loop.traces = append(loop.traces, event)
}

func (loop *Loop) reportPanicLocked(handle Handle, recovered any) {
	loop.errors = append(loop.errors, ErrorReport{
		Time:   loop.now,
		Handle: handle,
		Err:    fmt.Errorf("callback panic: %v", recovered),
	})
	loop.logger.Logf("callback error: handle=%d time=%d err=%v stack=%s", handle, loop.now, recovered, debug.Stack())
}

func (loop *Loop) invokeTask(fn TaskFn, ctx *Context) (recovered any) {
	defer func() { recovered = recover() }()
	fn(ctx)
	return
}

func (loop *Loop) invokeMicrotask(fn MicrotaskFn, ctx *Context, handle Handle) (recovered any) {
	defer func() { recovered = recover() }()
	fn(ctx)
	return
}

func invokeFrame(fn FrameFn, ctx *Context, now Time) (recovered any) {
	defer func() { recovered = recover() }()
	fn(ctx, now)
	return
}

func invokeIdle(fn IdleFn, ctx *Context, deadline Deadline) (recovered any) {
	defer func() { recovered = recover() }()
	fn(ctx, deadline)
	return
}
