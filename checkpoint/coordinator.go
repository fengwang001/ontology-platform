package checkpoint

import (
	"fmt"
	"sort"
)

func (c *Coordinator) triggerLocked() (uint64, error) {
	in := fmt.Sprintf("clock=%d", c.clock)
	if c.failed {
		err := &Error{Op: "trigger", Reason: ReasonCoordinatorFailed,
			Detail: "coordinator is failed; restore required before triggering"}
		c.logf("trigger", "in=%s out=reject reason=%s basis=%q", in, err.Reason, err.Detail)
		return 0, err
	}
	if n := c.inProgressCountLocked(); n >= c.cfg.MaxConcurrent {
		err := &Error{Op: "trigger", Reason: ReasonConcurrencyFull,
			Detail: fmt.Sprintf("in_progress=%d >= max_concurrent=%d", n, c.cfg.MaxConcurrent)}
		c.logf("trigger", "in=%s out=reject reason=%s basis=%q", in, err.Reason, err.Detail)
		return 0, err
	}
	if c.hasCompleted && c.clock-c.lastCompletedAt < c.cfg.MinInterval {
		err := &Error{Op: "trigger", Reason: ReasonIntervalTooShort,
			Detail: fmt.Sprintf("clock=%d - last_completed_at=%d = %d < min_interval=%d",
				c.clock, c.lastCompletedAt, c.clock-c.lastCompletedAt, c.cfg.MinInterval)}
		c.logf("trigger", "in=%s out=reject reason=%s basis=%q", in, err.Reason, err.Detail)
		return 0, err
	}
	id := c.nextID
	c.nextID++
	c.checkpoints[id] = &checkpoint{id: id, startedAt: c.clock, state: StateInProgress, confirmed: map[int]bool{}}
	c.logf("trigger", "in=%s out=accept id=%d basis=%q", in, id,
		fmt.Sprintf("not failed, in_progress < %d, interval ok; started_at=%d", c.cfg.MaxConcurrent, c.clock))
	return id, nil
}

func (c *Coordinator) confirmLocked(id uint64, task int) error {
	in := fmt.Sprintf("id=%d task=%d clock=%d", id, task, c.clock)
	reject := func(reason Reason, detail string) error {
		err := &Error{Op: "confirm", Reason: reason, Detail: detail}
		c.logf("confirm", "in=%s out=reject reason=%s basis=%q", in, reason, detail)
		return err
	}
	if task < 0 || task >= c.cfg.Tasks {
		return reject(ReasonTaskOutOfRange,
			fmt.Sprintf("task=%d not in [0,%d)", task, c.cfg.Tasks))
	}
	cp, ok := c.checkpoints[id]
	if !ok {
		return reject(ReasonNotFound, fmt.Sprintf("id=%d never allocated", id))
	}
	if cp.state != StateInProgress {
		return reject(ReasonAlreadyEnded,
			fmt.Sprintf("id=%d already %s", id, endedDesc(cp)))
	}
	if cp.confirmed[task] {
		return reject(ReasonDuplicateConfirm,
			fmt.Sprintf("task=%d already confirmed id=%d", task, id))
	}
	cp.confirmed[task] = true
	if len(cp.confirmed) < c.cfg.Tasks {
		c.logf("confirm", "in=%s out=accept basis=%q", in,
			fmt.Sprintf("confirmed=%d/%d, still in_progress", len(cp.confirmed), c.cfg.Tasks))
		return nil
	}
	c.completeLocked(cp)
	c.logf("confirm", "in=%s out=complete basis=%q", in,
		fmt.Sprintf("all %d tasks confirmed at clock=%d", c.cfg.Tasks, c.clock))
	return nil
}

func endedDesc(cp *checkpoint) string {
	if cp.state == StateCompleted {
		return fmt.Sprintf("completed at clock=%d", cp.completedAt)
	}
	return fmt.Sprintf("aborted(%s)", cp.abortReason)
}

func (c *Coordinator) completeLocked(cp *checkpoint) {
	cp.state = StateCompleted
	cp.completedAt = c.clock
	c.lastCompletedAt = c.clock
	c.hasCompleted = true
	c.consecutiveTimeouts = 0
	for _, other := range c.checkpoints {
		if other.state == StateInProgress && other.id < cp.id {
			other.state = StateAborted
			other.abortReason = AbortSubsumed
			c.logf("subsume", "id=%d aborted by completion of id=%d (not counted)", other.id, cp.id)
		}
	}
	c.retained = append(c.retained, cp.id)
	for len(c.retained) > c.cfg.Retention {
		evicted := c.retained[0]
		c.retained = c.retained[1:]
		c.logf("evict", "id=%d evicted from retained set (keep largest %d)", evicted, c.cfg.Retention)
	}
}

func (c *Coordinator) advanceLocked(t int64) error {
	in := fmt.Sprintf("t=%d clock=%d", t, c.clock)
	if t < c.clock {
		err := &Error{Op: "advance", Reason: ReasonClockBackward,
			Detail: fmt.Sprintf("t=%d < clock=%d", t, c.clock)}
		c.logf("advance", "in=%s out=reject reason=%s basis=%q", in, err.Reason, err.Detail)
		return err
	}
	c.clock = t
	for _, id := range c.inProgressIDsLocked() {
		cp := c.checkpoints[id]
		if c.clock < cp.startedAt+c.cfg.Timeout {
			continue
		}
		cp.state = StateAborted
		cp.abortReason = AbortTimeout
		c.consecutiveTimeouts++
		c.logf("timeout", "id=%d aborted at clock=%d basis=%q consecutive_timeouts=%d",
			id, c.clock,
			fmt.Sprintf("clock=%d >= started_at=%d + timeout=%d", c.clock, cp.startedAt, c.cfg.Timeout),
			c.consecutiveTimeouts)
		if c.consecutiveTimeouts > c.cfg.MaxConsecutiveTimeout {
			c.failLocked()
			break
		}
	}
	c.logf("advance", "in=%s out=ok clock=%d failed=%v consecutive_timeouts=%d",
		in, c.clock, c.failed, c.consecutiveTimeouts)
	return nil
}

func (c *Coordinator) failLocked() {
	c.failed = true
	for _, id := range c.inProgressIDsLocked() {
		cp := c.checkpoints[id]
		cp.state = StateAborted
		cp.abortReason = AbortFailed
		c.logf("fail", "id=%d aborted(failed) because consecutive_timeouts=%d > max=%d (not counted)",
			id, c.consecutiveTimeouts, c.cfg.MaxConsecutiveTimeout)
	}
	c.logf("fail", "coordinator failed: consecutive_timeouts=%d > max_consecutive_timeout=%d",
		c.consecutiveTimeouts, c.cfg.MaxConsecutiveTimeout)
}

func (c *Coordinator) restoreLocked() (uint64, error) {
	in := fmt.Sprintf("clock=%d", c.clock)
	if len(c.retained) == 0 {
		err := &Error{Op: "restore", Reason: ReasonNoCheckpoint,
			Detail: "retained set is empty"}
		c.logf("restore", "in=%s out=reject reason=%s basis=%q", in, err.Reason, err.Detail)
		return 0, err
	}
	id := c.retained[len(c.retained)-1]
	for _, otherID := range c.inProgressIDsLocked() {
		other := c.checkpoints[otherID]
		other.state = StateAborted
		other.abortReason = AbortRestored
		c.logf("restore", "id=%d aborted(restored) by restore to id=%d (not counted)", otherID, id)
	}
	c.consecutiveTimeouts = 0
	wasFailed := c.failed
	c.failed = false
	c.logf("restore", "in=%s out=ok restore_to=%d basis=%q", in, id,
		fmt.Sprintf("largest retained id; consecutive_timeouts reset; failed %v->false", wasFailed))
	return id, nil
}

func (c *Coordinator) snapshotLocked() Snapshot {
	snap := Snapshot{
		Clock:               c.clock,
		NextID:              c.nextID,
		Failed:              c.failed,
		ConsecutiveTimeouts: c.consecutiveTimeouts,
		InProgress:          c.inProgressIDsLocked(),
		Retained:            append([]uint64(nil), c.retained...),
	}
	return snap
}

func (c *Coordinator) getLocked(id uint64) (Checkpoint, bool) {
	cp, ok := c.checkpoints[id]
	if !ok {
		return Checkpoint{}, false
	}
	tasks := make([]int, 0, len(cp.confirmed))
	for task := range cp.confirmed {
		tasks = append(tasks, task)
	}
	sort.Ints(tasks)
	return Checkpoint{
		ID:          cp.id,
		StartedAt:   cp.startedAt,
		CompletedAt: cp.completedAt,
		State:       cp.state,
		AbortReason: cp.abortReason,
		Confirmed:   tasks,
	}, true
}

func (c *Coordinator) inProgressCountLocked() int {
	n := 0
	for _, cp := range c.checkpoints {
		if cp.state == StateInProgress {
			n++
		}
	}
	return n
}

func (c *Coordinator) inProgressIDsLocked() []uint64 {
	var ids []uint64
	for id, cp := range c.checkpoints {
		if cp.state == StateInProgress {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
