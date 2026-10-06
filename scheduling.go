package ontology

import "container/heap"

func (loop *Loop) materializeTimersLocked(target Time) {
	earliest := target
	if loop.renderRequestedLocked() && loop.nextFrame < earliest {
		earliest = loop.nextFrame
	}
	if loop.timers.Len() > 0 && loop.timers[0].due < earliest {
		earliest = loop.timers[0].due
	}
	for element := loop.idles.Front(); element != nil; element = element.Next() {
		item := element.Value.(*idleCallback)
		if item.hasTO && item.dueAt < earliest {
			earliest = item.dueAt
		}
	}
	if earliest > loop.now && loop.runnableCountLocked() == 0 && loop.microtasks.Len() == 0 {
		loop.now = earliest
		loop.advanceFrameLocked(true, target)
	}

	for loop.timers.Len() > 0 && loop.timers[0].due <= loop.now {
		timer := heap.Pop(&loop.timers).(*timerEntry)
		delete(loop.timerByID, timer.handle)
		if loop.state[timer.handle] == stateCanceled {
			continue
		}
		timer.created = true
		loop.pushTaskLocked(&task{
			handle: timer.handle,
			source: SourceTimer,
			enqAt:  timer.due,
			seq:    timer.seq,
			depth:  timer.depth,
			fn:     timer.fn,
		})
	}
	var due []*idleCallback
	for element := loop.idles.Front(); element != nil; element = element.Next() {
		idle := element.Value.(*idleCallback)
		if idle.hasTO && idle.dueAt != 0 && idle.dueAt <= loop.now && loop.state[idle.handle] == stateActive {
			due = append(due, idle)
		}
	}
	for _, idle := range due {
		callback := idle.fn
		originalHandle := idle.handle
		handle := loop.allocLocked()
		loop.state[originalHandle] = stateDone
		dueAt := idle.dueAt
		loop.pushTaskLocked(&task{
			handle:     handle,
			source:     SourceInternal,
			enqAt:      dueAt,
			seq:        loop.nextSequenceLocked(),
			depth:      0,
			idle:       idle,
			idleHandle: originalHandle,
			fn: func(ctx *Context) {
				callback(ctx, Deadline{Now: dueAt, Deadline: dueAt, Timeout: true})
			},
		})
		if element := loop.idleByID[originalHandle]; element != nil {
			loop.idles.Remove(element)
		}
		delete(loop.idleByID, originalHandle)
	}
}

func nextBoundary(now, interval Time) Time {
	return (now/interval + 1) * interval
}

func (loop *Loop) advanceFrameLocked(renderReady bool, target Time) {
	for loop.now >= loop.nextFrame {
		if renderReady || loop.renderRequestedLocked() || loop.now == loop.nextFrame && loop.activeIdleCountLocked() > 0 {
			return
		}
		if loop.now == loop.nextFrame {
			return
		}
		if loop.nextFrame >= target {
			return
		}
		loop.nextFrame += loop.config.FrameInterval
	}
}

func (loop *Loop) runMicrotasksLocked() {
	for loop.microtasks.Len() > 0 {
		element := loop.microtasks.Front()
		item := element.Value.(*microtask)
		loop.microtasks.Remove(element)
		delete(loop.microByID, item.handle)
		if loop.state[item.handle] == stateCanceled {
			continue
		}
		loop.state[item.handle] = stateDone
		ctx := &Context{loop: loop, depth: item.depth, handle: item.handle}
		loop.appendTraceLocked(TraceEvent{Type: EventMicrotask, Handle: item.handle, Time: loop.now, Depth: item.depth})
		loop.mu.Unlock()
		recovered := loop.invokeMicrotask(item.fn, ctx, item.handle)
		loop.mu.Lock()
		if recovered != nil {
			loop.reportPanicLocked(item.handle, recovered)
		}
	}
}

func (loop *Loop) renderRequestedLocked() bool {
	if loop.dirty {
		return true
	}
	for element := loop.frames.Front(); element != nil; element = element.Next() {
		return true
	}
	return false
}

func missedBoundary(now, interval Time) Time {
	if now%interval == 0 {
		return now
	}
	return (now/interval + 1) * interval
}

func (loop *Loop) render(now Time) {
	callbacks := make([]*frameCallback, 0, loop.frames.Len())
	loop.mu.Lock()
	for loop.frames.Len() > 0 {
		element := loop.frames.Front()
		item := element.Value.(*frameCallback)
		loop.frames.Remove(element)
		delete(loop.frameByID, item.handle)
		if loop.state[item.handle] != stateCanceled {
			callbacks = append(callbacks, item)
			loop.state[item.handle] = stateDone
		}
	}
	loop.dirty = false
	loop.appendTraceLocked(TraceEvent{Type: EventRender, Time: now})
	loop.mu.Unlock()

	for _, item := range callbacks {
		ctx := &Context{loop: loop}
		loop.mu.Lock()
		loop.appendTraceLocked(TraceEvent{Type: EventFrame, Handle: item.handle, Time: now})
		loop.mu.Unlock()
		recovered := invokeFrame(item.fn, ctx, now)
		loop.mu.Lock()
		if recovered != nil {
			loop.reportPanicLocked(item.handle, recovered)
		}
		loop.runMicrotasksLocked()
		loop.mu.Unlock()
	}
}

func (loop *Loop) canIdleLocked(target Time) bool {
	if loop.runnableCountLocked() > 0 || loop.microtasks.Len() > 0 || loop.renderRequestedLocked() {
		return false
	}
	if loop.now < loop.nextFrame && loop.nextFrame <= target {
		return loop.nextFrame-loop.now > 0 && loop.activeIdleCountLocked() > 0
	}
	return false
}

func (loop *Loop) hasTaskUntilLocked(deadline Time) bool {
	for source := SourceUser; source < sourceCount; source++ {
		loop.cleanHead(source)
		if loop.queues[source].tasks.Len() > 0 && loop.queues[source].tasks.Front().Value.(*task).enqAt <= deadline {
			return true
		}
	}
	if loop.timers.Len() > 0 && loop.timers[0].due <= deadline {
		return true
	}
	return false
}

func (loop *Loop) activeIdleCountLocked() int {
	count := 0
	nextEpoch := uint64(1)
	for element := loop.idles.Front(); element != nil; element = element.Next() {
		item := element.Value.(*idleCallback)
		if item.bornEpoch != nextEpoch && loop.state[item.handle] == stateActive && (!item.hasTO || item.dueAt > loop.now) {
			count++
		}
	}
	return count
}

func (loop *Loop) runIdlePeriodLocked(target Time) {
	deadline := loop.nextFrame
	epoch := uint64(1)
	loop.idleEpoch = epoch
	pending := make([]*idleCallback, 0)
	for element := loop.idles.Front(); element != nil; element = element.Next() {
		item := element.Value.(*idleCallback)
		if item.bornEpoch != epoch && loop.state[item.handle] == stateActive && (!item.hasTO || item.dueAt > loop.now) {
			pending = append(pending, item)
		}
	}
	for _, item := range pending {
		if loop.state[item.handle] != stateActive {
			continue
		}
		if element, ok := loop.idleByID[item.handle]; ok {
			loop.idles.Remove(element)
			delete(loop.idleByID, item.handle)
		}
		loop.state[item.handle] = stateDone
		info := Deadline{Now: loop.now, Deadline: deadline, Timeout: item.dueAt != 0 && item.dueAt <= deadline}
		ctx := &Context{loop: loop}
		loop.appendTraceLocked(TraceEvent{Type: EventIdle, Handle: item.handle, Time: loop.now, Remain: deadline - loop.now, Deadline: deadline})
		loop.mu.Unlock()
		recovered := invokeIdle(item.fn, ctx, info)
		loop.mu.Lock()
		if recovered != nil {
			loop.reportPanicLocked(item.handle, recovered)
		}
		loop.runMicrotasksLocked()
		if loop.runnableCountLocked() > 0 || loop.microtasks.Len() > 0 || loop.renderRequestedLocked() {
			loop.idleEpoch = 0
			return
		}
	}
	loop.idleEpoch = 0
}
