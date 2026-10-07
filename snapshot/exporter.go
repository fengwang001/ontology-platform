package snapshot

import "slices"

type Exporter struct {
	events []Event
}

func NewExporter(events []Event) *Exporter { return &Exporter{events: events} }

func (e *Exporter) Export(requestedBoundary int64, limits Limits) ([]ExportFrame, error) {
	boundaries := NewBoundaryManager()
	commitLSNs := make([]int64, 0)
	for _, event := range e.events {
		if event.Type == EventCommit {
			commitLSNs = append(commitLSNs, event.CommitLSN)
		}
	}
	boundary, err := boundaries.Resolve(requestedBoundary, commitLSNs)
	if err != nil {
		return nil, err
	}

	validator := NewValidator()
	state := newSnapshot(boundary)
	frames := make([]ExportFrame, 0)
	snapshotEmitted := false
	if boundary == 0 {
		frames = append(frames, ExportFrame{Snapshot: cloneSnapshot(state)})
		snapshotEmitted = true
	}

	for _, event := range e.events {
		if event.Type == EventPrepare || event.Type == EventAbort {
			if err := validator.ValidateEvent(event, limits); err != nil {
				return append(frames, ExportFrame{Error: exportErrorPtr(err)}), err
			}
			continue
		}
		if event.Type != EventCommit {
			err := boundaryError("event for transaction %q has unknown type %q", event.TxID, event.Type)
			return append(frames, ExportFrame{Error: exportErrorPtr(err)}), err
		}

		records := validator.TransactionRecords(event.TxID)
		if err := validator.ValidateEvent(event, limits); err != nil {
			return append(frames, ExportFrame{Error: exportErrorPtr(err)}), err
		}
		if err := validator.CheckIntegrity(state, event.TxID, records); err != nil {
			return append(frames, ExportFrame{Error: exportErrorPtr(err)}), err
		}
		validator.CompleteCommit(event.TxID)
		if err := validator.CheckResourceLimits(limits); err != nil {
			return append(frames, ExportFrame{Error: exportErrorPtr(err)}), err
		}

		inSnapshot := boundaries.Classify(event.CommitLSN, boundary)
		for _, record := range records {
			applyRecord(state, record)
		}
		if inSnapshot {
			if event.CommitLSN == boundary {
				frames = append(frames, ExportFrame{Snapshot: cloneSnapshot(state)})
				snapshotEmitted = true
			}
			continue
		}
		if !snapshotEmitted {
			err := boundaryError("commit %d arrived before the snapshot boundary %d was emitted", event.CommitLSN, boundary)
			return append(frames, ExportFrame{Error: exportErrorPtr(err)}), err
		}
		frames = append(frames, ExportFrame{
			Deltas: []Delta{{
				CommitLSN: event.CommitLSN,
				TxID:      event.TxID,
				Records:   slices.Clone(records),
			}},
		})
	}
	for _, event := range e.events {
		if event.Type == EventPrepare && len(validator.open[event.TxID]) > 0 {
			err := atomicityError(0, event.TxID, "transaction %q has prepared records without commit or abort", event.TxID)
			return append(frames, ExportFrame{Error: exportErrorPtr(err)}), err
		}
	}

	if !snapshotEmitted {
		err := boundaryError("snapshot boundary %d was never reached in accepted commits", boundary)
		return append(frames, ExportFrame{Error: exportErrorPtr(err)}), err
	}
	frames = append(frames, ExportFrame{Done: true})
	return frames, nil
}

func exportErrorPtr(err error) *ExportError {
	if typed, ok := err.(*ExportError); ok {
		return typed
	}
	return &ExportError{Class: ClassResource, Message: err.Error()}
}
