package store

import (
	"context"
	"errors"
	"fmt"
)

func intentObjectID(in intentRecord) string { return in.ObjectID }

func (s *Store) guardErr(id string) (string, error) {
	if err := s.guardObject(id); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) readIntent(batchID string, seq int) (intentRecord, error) {
	b, err := s.engine.Get(keyIntent(batchID, seq))
	if err != nil {
		return intentRecord{}, err
	}
	return decode[intentRecord](b)
}

func (s *Store) intentCount(batchID string) int {
	l, ok := s.engine.(Lister)
	if !ok {
		return 0
	}
	prefix := "b/" + hexID(batchID) + "/i/"
	return len(l.ListKeys(prefix))
}

// recover inspects exactly one batch: the one named by the durable
// "active" pointer. The amount of work is O(size of that batch); it is
// independent of the total number of batches ever processed.
func (s *Store) recover(ctx context.Context) (*RecoveryReport, error) {
	ptr, err := s.engine.Get(keyActiveBatch)
	if errors.Is(err, ErrNotFound) || string(ptr) == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	batchID := string(ptr)
	report := &RecoveryReport{BatchID: batchID}
	report.Steps = append(report.Steps, RecoveryStep{
		Barrier:  barrierActivePointer,
		Evidence: "active=" + batchID,
		Decision: "inspect batch marker",
	})

	stateBytes, err := s.engine.Get(keyBatchState(batchID))
	if errors.Is(err, ErrNotFound) {
		// Pointer durable, state record never made it: the unique commit
		// point does not exist, so the batch is classified as not having
		// taken effect.
		count := s.intentCount(batchID)
		report.Class = ClassRecoveredAbort
		report.RecordsScanned = 2 + count
		report.Steps = append(report.Steps, RecoveryStep{
			Barrier:  barrierStatePrepared,
			Evidence: "state marker absent",
			Decision: "commit point absent -> roll back",
		})
		if err := s.rollback(ctx, batchID, count, true); err != nil {
			return nil, err
		}
		return report, nil
	}
	if err != nil {
		return nil, err
	}
	st, err := decode[stateRecord](stateBytes)
	if err != nil {
		return nil, err
	}

	switch st.State {
	case statePrepared:
		report.Class = ClassRecoveredAbort
		report.RecordsScanned = 2 + st.Count
		report.Steps = append(report.Steps, RecoveryStep{
			Barrier:  barrierStatePrepared,
			Evidence: "state=PREPARED",
			Decision: "before commit point -> roll back",
		})
		if err := s.rollback(ctx, batchID, st.Count, true); err != nil {
			return nil, err
		}

	case stateCommitted:
		report.Class = ClassRecoveredCommit
		report.RecordsScanned = 2 + st.Count
		report.Steps = append(report.Steps, RecoveryStep{
			Barrier:  barrierStateCommitted,
			Evidence: "state=COMMITTED",
			Decision: "at/after commit point -> roll forward",
		})
		if err := s.rollforward(ctx, batchID, st.Count); err != nil {
			return nil, err
		}
		if err := s.journalAppend("recovered_commit", batchID, fmt.Sprintf("count=%d", st.Count)); err != nil {
			return nil, err
		}

	case stateCommitDone:
		// Roll-forward completed; only cleanup may remain. The batch is
		// definitively committed.
		count := st.Count
		if count == 0 {
			count = s.intentCount(batchID)
		}
		report.RecordsScanned = 2 + count
		report.Steps = append(report.Steps, RecoveryStep{
			Barrier:  barrierStateDone + ".commit",
			Evidence: "state=COMMIT_DONE",
			Decision: "complete remaining cleanup only",
		})
		report.Class = ClassRecoveredCommit
		if err := s.resumeCleanup(batchID, st.Count); err != nil {
			return nil, err
		}
		if err := s.journalAppend("recovered_commit", batchID, fmt.Sprintf("count=%d cleanup-only", count)); err != nil {
			return nil, err
		}

	case stateAbortDone:
		// Roll-back completed; only cleanup may remain. The batch is
		// definitively not effective.
		count := st.Count
		if count == 0 {
			count = s.intentCount(batchID)
		}
		report.RecordsScanned = 2 + count
		report.Steps = append(report.Steps, RecoveryStep{
			Barrier:  barrierStateDone + ".abort",
			Evidence: "state=ABORT_DONE",
			Decision: "complete remaining cleanup only",
		})
		report.Class = ClassRecoveredAbort
		if err := s.resumeCleanup(batchID, st.Count); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("store: unknown batch state %q", st.State)
	}

	return report, nil
}

// resumeCleanup finishes a DONE batch: delete leftover intents first and
// clear the pointer last, so a crash mid-cleanup still names the batch and
// cleanup simply repeats (Delete is idempotent). Never reinstalls.
func (s *Store) resumeCleanup(batchID string, count int) error {
	if count == 0 {
		count = s.intentCount(batchID)
	}
	for i := 0; i < count; i++ {
		if err := s.engine.Delete(keyIntent(batchID, i)); err != nil {
			return err
		}
	}
	return s.engine.Put(keyActiveBatch, []byte(""))
}
