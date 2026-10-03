package handler

import (
	"fmt"

	"ontology/dedupe"
	"ontology/history"
)

// Step applies the head queued update, appends A, and returns its result.
func (h *Handler) Step(inst []byte) (Result, error) {
	if len(inst) == 0 {
		return Result{}, fmt.Errorf("%w: empty instance id", ErrInvalidParam)
	}
	in, err := h.lookup(inst)
	if err != nil {
		return Result{}, err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(in.queue) == 0 {
		return Result{}, ErrEmpty
	}
	head := in.queue[0]
	in.queue = in.queue[1:]
	in.applied += head.delta
	in.pendingSum -= head.delta
	if _, err := in.log.AppendApplied(head.uSeq); err != nil {
		return Result{}, err
	}
	head.result.Kind = Completed
	head.result.Value = in.applied
	return *head.result, nil
}

// Close idempotently closes an instance, aborting queued updates.
func (h *Handler) Close(inst []byte) error {
	if len(inst) == 0 {
		return fmt.Errorf("%w: empty instance id", ErrInvalidParam)
	}
	in, err := h.lookup(inst)
	if err != nil {
		return err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return nil
	}
	if _, err := in.log.AppendClosed(); err != nil {
		return err
	}
	in.closed = true
	for _, q := range in.queue {
		q.result.Kind = Aborted
	}
	in.queue = nil
	in.pendingSum = 0
	return nil
}

// ResultOf returns the registered result for uid, or Unknown.
func (h *Handler) ResultOf(inst []byte, uid string) Result {
	in, err := h.lookup(inst)
	if err != nil {
		return Result{Kind: Unknown}
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if r, ok := in.table.Get(uid); ok {
		return *r
	}
	return Result{Kind: Unknown}
}

// Recover discards in-memory state and rebuilds it from the history log:
// the first m U events (m = number of A events) are applied, the rest are
// queued; a trailing C aborts whatever is still queued. The dedup table is
// rebuilt by re-registering the uids of all U events in seq order, so it
// ends up holding the most recent K of them.
func (h *Handler) Recover(inst []byte) error {
	if len(inst) == 0 {
		return fmt.Errorf("%w: empty instance id", ErrInvalidParam)
	}
	key := string(inst)
	h.mu.Lock()
	in, ok := h.insts[key]
	if !ok {
		h.mu.Unlock()
		return ErrNotFound
	}
	cfg := h.configs[key]
	events := in.log.Events(in.log.Len())
	h.mu.Unlock()

	fresh, err := rebuild(cfg, events)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.insts[key] = fresh
	h.mu.Unlock()
	return nil
}

// rebuild derives runtime state purely from a history prefix.
func rebuild(cfg config, events []history.Event) (*instance, error) {
	table, err := dedupe.New[*Result](cfg.k)
	if err != nil {
		return nil, err
	}
	in := &instance{cfg: cfg, log: history.New(), table: table}
	var us []history.Event
	appliedCount := 0
	for i, e := range events {
		if in.closed {
			return nil, fmt.Errorf("%w: event after close at seq %d", ErrCorruptHistory, e.Seq)
		}
		if e.Seq != int64(i)+1 {
			return nil, fmt.Errorf("%w: seq gap at %d", ErrCorruptHistory, e.Seq)
		}
		switch e.Kind {
		case history.UpdateAccepted:
			if _, err := in.log.AppendUpdate(e.UID, e.Delta); err != nil {
				return nil, err
			}
			us = append(us, e)
		case history.UpdateApplied:
			appliedCount++
			if appliedCount > len(us) || us[appliedCount-1].Seq != e.Ref {
				return nil, fmt.Errorf("%w: bad A ref %d", ErrCorruptHistory, e.Ref)
			}
			if _, err := in.log.AppendApplied(e.Ref); err != nil {
				return nil, err
			}
		case history.Closed:
			if _, err := in.log.AppendClosed(); err != nil {
				return nil, err
			}
			in.closed = true
		}
	}
	// The queue is strictly FIFO, so the first appliedCount U events are the
	// applied ones, in order.
	for i, u := range us {
		res := &Result{Seq: u.Seq}
		if i < appliedCount {
			in.applied += u.Delta
			res.Kind = Completed
			res.Value = in.applied
		} else {
			res.Kind = Accepted
			in.queue = append(in.queue, queued{delta: u.Delta, uSeq: u.Seq, result: res})
			in.pendingSum += u.Delta
		}
		in.table.Add(u.UID, res)
	}
	if in.applied < 0 || in.applied > cfg.maxCap {
		return nil, fmt.Errorf("%w: applied value %d out of range", ErrCorruptHistory, in.applied)
	}
	if in.closed {
		for _, q := range in.queue {
			q.result.Kind = Aborted
		}
		in.queue = nil
		in.pendingSum = 0
	}
	return in, nil
}

// History returns a copy of the instance's full event log (test support).
func (h *Handler) History(inst []byte) []history.Event {
	in, err := h.lookup(inst)
	if err != nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.log.Events(in.log.Len())
}
