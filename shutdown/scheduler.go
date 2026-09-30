package shutdown

import "container/heap"

type killEvent struct {
	at       int64
	sequence int64
	id       string
}

type killHeap struct {
	items    []killEvent
	sequence int64
}

func newKillHeap() *killHeap {
	return &killHeap{}
}

func (h *killHeap) len() int {
	return len(h.items)
}

func (h *killHeap) push(at int64, id string) {
	h.sequence++
	heap.Push(h, killEvent{at: at, sequence: h.sequence, id: id})
}

func (h *killHeap) peek() (killEvent, bool) {
	if len(h.items) == 0 {
		return killEvent{}, false
	}
	return h.items[0], true
}

func (h *killHeap) pop() (killEvent, bool) {
	if len(h.items) == 0 {
		return killEvent{}, false
	}
	event := heap.Pop(h).(killEvent)
	return event, true
}

func (h *killHeap) Len() int {
	return len(h.items)
}

func (h *killHeap) Less(i, j int) bool {
	if h.items[i].at != h.items[j].at {
		return h.items[i].at < h.items[j].at
	}
	if h.items[i].id != h.items[j].id {
		return h.items[i].id < h.items[j].id
	}
	return h.items[i].sequence < h.items[j].sequence
}

func (h *killHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

func (h *killHeap) Push(value any) {
	h.items = append(h.items, value.(killEvent))
}

func (h *killHeap) Pop() any {
	last := len(h.items) - 1
	event := h.items[last]
	h.items = h.items[:last]
	return event
}

func (o *Orchestrator) settleUntil(at int64) {
	for {
		event, ok := o.kills.peek()
		if !ok || event.at > at {
			return
		}
		o.kills.pop()

		svc := o.services[event.id]
		if svc == nil {
			continue
		}
		if svc.stopped {
			o.logger.Info("stale kill skipped", "id", svc.id, "deadline_at", event.at, "reason", "already stopped")
			continue
		}
		if !svc.terminated {
			o.logger.Info("stale kill skipped", "id", svc.id, "deadline_at", event.at, "reason", "not terminated")
			continue
		}

		o.logger.Info("kill deadline reached", "id", svc.id, "deadline_at", event.at)
		o.stopService(svc, event.at, Killed)
	}
}

func (o *Orchestrator) terminateService(svc *service, at int64) {
	if svc.terminated {
		return
	}
	svc.terminated = true
	svc.terminationAt = at
	deadline := at + svc.gracePeriodMS
	o.kills.push(deadline, svc.id)
	o.logger.Info("termination signaled", "id", svc.id, "termination_at", at, "kill_deadline_at", deadline)
}

func (o *Orchestrator) stopService(svc *service, at int64, method StopMethod) {
	if svc.stopped {
		return
	}
	svc.stopped = true
	svc.stoppedAt = at
	svc.stopMethod = method
	methodName := "graceful"
	if method == Killed {
		methodName = "killed"
	}
	o.logger.Info("service stopped", "id", svc.id, "stop_at", at, "method", methodName)
	o.maybeSignalDependencies(svc, at)
}

func (o *Orchestrator) maybeSignalDependencies(stopped *service, at int64) {
	for dependencyID := range stopped.dependencies {
		dependency := o.services[dependencyID]
		if dependency.terminated {
			continue
		}
		ready := true
		for dependentID := range dependency.dependents {
			dependent := o.services[dependentID]
			if !dependent.stopped {
				ready = false
				break
			}
		}
		if ready {
			o.terminateService(dependency, at)
		}
	}
}
