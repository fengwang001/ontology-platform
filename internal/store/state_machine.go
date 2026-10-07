package store

import (
	"context"
	"errors"
	"fmt"
)

// Durability barriers. A fault injected at each barrier simulates process
// death at exactly that phase boundary; the enumeration test exercises
// every one of them.
const (
	barrierIntent         = "intent.write"
	barrierActivePointer  = "active.pointer.write"
	barrierStatePrepared  = "state.prepared.write"
	barrierStateCommitted = "state.committed.write"
	barrierInstanceApply  = "instance.apply"
	barrierStateDone      = "state.done.write"
	barrierActiveClear    = "active.pointer.clear"
	barrierIntentCleanup  = "intent.cleanup"
)

const (
	statePrepared   = "PREPARED"
	stateCommitted  = "COMMITTED"
	stateCommitDone = "COMMIT_DONE"
	stateAbortDone  = "ABORT_DONE"
)

func cloneProps(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// prepare performs phase 1: capture preimages and durably stage every
// intent. No instance is modified in this phase.
func (s *Store) prepare(batchID string, ops []Op) error {
	if len(ops) == 0 {
		return errors.New("store: empty batch")
	}

	seen := make(map[string]bool, len(ops))
	intents := make([]intentRecord, 0, len(ops))
	for _, op := range ops {
		if op.ObjectID == "" {
			return errors.New("store: op missing object id")
		}
		if seen[op.ObjectID] {
			return fmt.Errorf("store: object %q appears more than once in batch", op.ObjectID)
		}
		seen[op.ObjectID] = true
		if _, err := s.guardErr(op.ObjectID); err != nil {
			return err
		}

		cur, err := s.readInstance(op.ObjectID)
		if err != nil {
			return err
		}
		props := cloneProps(op.Properties)
		intents = append(intents, intentRecord{
			Kind:       "intent",
			BatchID:    batchID,
			ObjectID:   op.ObjectID,
			Seq:        len(intents),
			OldVersion: cur.Version,
			NewVersion: cur.Version + 1,
			OldProps:   cur.Properties,
			NewProps:   props,
		})
	}

	for i := range intents {
		b, err := encode(intents[i])
		if err != nil {
			return err
		}
		if err := s.barrier(fmt.Sprintf("%s.%d", barrierIntent, i), keyIntent(batchID, i), b); err != nil {
			return err
		}
	}

	s.inflight = batchID
	for _, in := range intents {
		s.objects[idOfIntent(in)] = batchID
	}

	ptr := []byte(batchID)
	if err := s.barrier(barrierActivePointer, keyActiveBatch, ptr); err != nil {
		return err
	}

	st := stateRecord{Kind: "state", BatchID: batchID, State: statePrepared, Count: len(intents)}
	b, err := encode(st)
	if err != nil {
		return err
	}
	if err := s.barrier(barrierStatePrepared, keyBatchState(batchID), b); err != nil {
		return err
	}
	return s.journalAppend("prepared", batchID, fmt.Sprintf("count=%d", len(intents)))
}

func idOfIntent(in intentRecord) string {
	return in.ObjectID
}

// commit performs phase 2 and 3: the single commit-point record followed
// by idempotent roll-forward installation.
func (s *Store) commit(ctx context.Context, batchID string, count int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := encode(stateRecord{Kind: "state", BatchID: batchID, State: stateCommitted, Count: count})
	if err != nil {
		return err
	}
	if err := s.barrier(barrierStateCommitted, keyBatchState(batchID), b); err != nil {
		return err
	}
	if err := s.journalAppend("commit_point", batchID, "externally determined committed"); err != nil {
		return err
	}
	return s.rollforward(ctx, batchID, count)
}

func (s *Store) rollforward(ctx context.Context, batchID string, count int) error {
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		in, err := s.readIntent(batchID, i)
		if err != nil {
			return err
		}
		inst := &Instance{
			ID:         intentObjectID(in),
			Version:    in.NewVersion,
			Properties: cloneProps(in.NewProps),
		}
		b, err := encode(instanceRecord{Kind: "instance", ID: inst.ID, Version: inst.Version, Properties: inst.Properties})
		if err != nil {
			return err
		}
		if err := s.barrier(fmt.Sprintf("%s.%d", barrierInstanceApply, i), keyInstance(inst.ID), b); err != nil {
			return err
		}
	}
	return s.finalize(batchID, count)
}

func (s *Store) finalize(batchID string, count int) error {
	b, err := encode(stateRecord{Kind: "state", BatchID: batchID, State: stateCommitDone, Count: count})
	if err != nil {
		return err
	}
	if err := s.barrier(barrierStateDone+".commit", keyBatchState(batchID), b); err != nil {
		return err
	}

	for i := 0; i < count; i++ {
		if err := s.engine.Delete(keyIntent(batchID, i)); err != nil {
			return err
		}
		if err := s.crashGate(barrierIntentCleanup + "." + fmt.Sprint(i)); err != nil {
			return err
		}
	}

	// Pointer is cleared last: a crash at any earlier point still names a
	// DONE batch, and resumeCleanup repeats idempotently.
	if err := s.barrier(barrierActiveClear, keyActiveBatch, []byte("")); err != nil {
		return err
	}
	s.clearLive(batchID)
	if err := s.journalAppend("done", batchID, fmt.Sprintf("count=%d", count)); err != nil {
		return err
	}
	return nil
}

func (s *Store) crashGate(barrier string) error {
	if s.hook == nil {
		return nil
	}
	if err := s.hook(barrier); err != nil {
		var ce *CrashError
		if errors.As(err, &ce) {
			return s.crashf(ce.Barrier)
		}
		return err
	}
	return nil
}

func (s *Store) clearLive(batchID string) {
	if s.inflight == batchID {
		s.inflight = ""
	}
	for obj, b := range s.objects {
		if b == batchID {
			delete(s.objects, obj)
		}
	}
}

// rollback performs the normal abort path. No commit record exists, so
// every instance remains at its preimage; staged intents are discarded.
func (s *Store) rollback(ctx context.Context, batchID string, count int, recovered bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Verify (never mutate) that each instance still equals its preimage.
	for i := 0; i < count; i++ {
		in, err := s.readIntent(batchID, i)
		if err != nil {
			return err
		}
		cur, err := s.readInstance(intentObjectID(in))
		if err != nil {
			return err
		}
		if cur.Version != in.OldVersion || !propsEqual(cur.Properties, in.OldProps) {
			return fmt.Errorf("store: internal error, instance %q changed during pre-commit window", intentObjectID(in))
		}
	}

	if err := s.barrier(barrierStateDone+".abort", keyBatchState(batchID),
		mustEncode(stateRecord{Kind: "state", BatchID: batchID, State: stateAbortDone, Count: count})); err != nil {
		return err
	}
	for i := 0; i < count; i++ {
		if err := s.engine.Delete(keyIntent(batchID, i)); err != nil {
			return err
		}
		if err := s.crashGate(barrierIntentCleanup + "." + fmt.Sprint(i)); err != nil {
			return err
		}
	}
	if err := s.barrier(barrierActiveClear, keyActiveBatch, []byte("")); err != nil {
		return err
	}
	s.clearLive(batchID)
	phase := "aborted"
	if recovered {
		phase = "recovered_abort"
	}
	return s.journalAppend(phase, batchID, fmt.Sprintf("count=%d", count))
}

func mustEncode(v any) []byte {
	b, err := encode(v)
	if err != nil {
		panic(err)
	}
	return b
}

func propsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
