package snapshot

import (
	"fmt"
	"strings"
)

type Validator struct {
	committed     map[string]bool
	open          map[string][]Record
	lastCommitLSN int64
	transactions  int
	buffered      int
	accepted      int
}

func NewValidator() *Validator {
	return &Validator{
		committed: map[string]bool{},
		open:      map[string][]Record{},
	}
}

func ValidateHistory(events []Event, limits Limits) (*Validator, error) {
	validator := NewValidator()
	for _, event := range events {
		if err := validator.ValidateEvent(event, limits); err != nil {
			return nil, err
		}
		if event.Type == EventCommit {
			validator.CompleteCommit(event.TxID)
			if err := validator.CheckResourceLimits(limits); err != nil {
				return nil, err
			}
		}
	}
	return validator, nil
}

func NewValidatorWithState(lastCommitLSN int64, transactions, records int) *Validator {
	return &Validator{
		committed:     map[string]bool{},
		open:          map[string][]Record{},
		lastCommitLSN: lastCommitLSN,
		transactions:  transactions,
		accepted:      records,
	}
}

func (v *Validator) ValidateEvent(event Event, limits Limits) error {
	switch event.Type {
	case EventPrepare:
		if event.CommitLSN != 0 {
			return boundaryError("prepare event for transaction %q must not carry a commit LSN", event.TxID)
		}
		if strings.TrimSpace(event.TxID) == "" {
			return atomicityError(0, "", "event has an empty transaction id")
		}
		if event.Record == nil {
			return atomicityError(0, event.TxID, "prepare event has no record")
		}
		if err := validateRecord(*event.Record); err != nil {
			return err
		}
		if v.committed[event.TxID] {
			return atomicityError(0, event.TxID, "record arrived after transaction commit")
		}
		if _, exists := v.open[event.TxID]; !exists {
			v.open[event.TxID] = nil
		}
		v.open[event.TxID] = append(v.open[event.TxID], *event.Record)
		v.buffered++
	case EventCommit:
		if event.CommitLSN <= 0 {
			return boundaryError("commit event for transaction %q has no positive commit LSN", event.TxID)
		}
		if event.CommitLSN != v.lastCommitLSN+1 {
			return boundaryError("commit LSN %d does not immediately follow %d", event.CommitLSN, v.lastCommitLSN)
		}
		if v.committed[event.TxID] {
			return atomicityError(event.CommitLSN, event.TxID, "transaction was already committed")
		}
		if _, exists := v.open[event.TxID]; !exists {
			return atomicityError(event.CommitLSN, event.TxID, "commit has no prepared records")
		}
		if event.Record != nil {
			return atomicityError(event.CommitLSN, event.TxID, "commit event must not contain a record")
		}
		v.lastCommitLSN = event.CommitLSN
		v.committed[event.TxID] = true
		v.transactions++
	case EventAbort:
		if event.CommitLSN != 0 {
			return boundaryError("abort event for transaction %q must not carry a commit LSN", event.TxID)
		}
		if event.Record != nil {
			return atomicityError(0, event.TxID, "abort event must not contain a record")
		}
		if strings.TrimSpace(event.TxID) == "" {
			return atomicityError(0, "", "event has an empty transaction id")
		}
		if _, exists := v.open[event.TxID]; !exists {
			return atomicityError(0, event.TxID, "abort has no active transaction")
		}
		v.buffered -= len(v.open[event.TxID])
		delete(v.open, event.TxID)
	default:
		return boundaryError("event for transaction %q has unknown type %q", event.TxID, event.Type)
	}
	return nil
}

func (v *Validator) TransactionRecords(txID string) []Record {
	return append([]Record(nil), v.open[txID]...)
}

func (v *Validator) CompleteCommit(txID string) {
	v.accepted += len(v.open[txID])
	v.buffered -= len(v.open[txID])
	delete(v.open, txID)
}

func (v *Validator) CheckResourceLimits(limits Limits) error {
	return checkResourceLimits(v.transactions, v.accepted, limits)
}

func (v *Validator) CheckIntegrity(snap *Snapshot, txID string, records []Record) error {
	next := cloneSnapshot(snap)
	for _, record := range records {
		applyRecord(next, record)
	}
	for _, link := range next.Links {
		if _, sourceExists := next.Objects[link.SourceID]; !sourceExists {
			return integrityError(link.ID, txID, "link %q references missing source object %q", link.ID, link.SourceID)
		}
		if _, targetExists := next.Objects[link.TargetID]; !targetExists {
			return integrityError(link.ID, txID, "link %q references missing target object %q", link.ID, link.TargetID)
		}
	}
	return nil
}

func (v *Validator) OpenTransactions() map[string][]Record { return v.open }

func validateRecord(record Record) error {
	if strings.TrimSpace(record.ID) == "" {
		return atomicityError(0, "", "record has an empty id")
	}
	switch record.Kind {
	case KindObject, KindAction:
		if record.SourceID != "" || record.TargetID != "" {
			return integrityError(record.ID, "", "non-link record %q has link endpoints", record.ID)
		}
	case KindLink:
		if strings.TrimSpace(record.SourceID) == "" || strings.TrimSpace(record.TargetID) == "" {
			return integrityError(record.ID, "", "link %q requires both endpoints", record.ID)
		}
	default:
		return boundaryError("record %q has unknown kind %q", record.ID, record.Kind)
	}
	switch record.Operation {
	case OpUpsert, OpDelete:
	default:
		return boundaryError("record %q has unknown operation %q", record.ID, record.Operation)
	}
	return nil
}

func checkResourceLimits(transactions, records int, limits Limits) error {
	if limits.MaxTransactions >= 0 && transactions > limits.MaxTransactions {
		return resourceError("transaction count %d exceeds limit %d", transactions, limits.MaxTransactions)
	}
	if limits.MaxRecords >= 0 && records > limits.MaxRecords {
		return resourceError("record count %d exceeds limit %d", records, limits.MaxRecords)
	}
	return nil
}

func applyRecord(snap *Snapshot, record Record) {
	target := snap.recordMap(record.Kind)
	if record.Operation == OpDelete {
		delete(target, record.ID)
		return
	}
	target[record.ID] = record
}

func (s *Snapshot) recordMap(kind RecordKind) map[string]Record {
	switch kind {
	case KindObject:
		return s.Objects
	case KindLink:
		return s.Links
	default:
		return s.Actions
	}
}

func cloneSnapshot(source *Snapshot) *Snapshot {
	if source == nil {
		return newSnapshot(0)
	}
	return &Snapshot{
		Boundary: source.Boundary,
		Objects:  cloneRecordMap(source.Objects),
		Links:    cloneRecordMap(source.Links),
		Actions:  cloneRecordMap(source.Actions),
	}
}

func cloneRecordMap(source map[string]Record) map[string]Record {
	result := make(map[string]Record, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func newSnapshot(boundary int64) *Snapshot {
	return &Snapshot{
		Boundary: boundary,
		Objects:  map[string]Record{},
		Links:    map[string]Record{},
		Actions:  map[string]Record{},
	}
}

func atomicityError(commitLSN int64, txID, format string, args ...any) *ExportError {
	return &ExportError{
		Class:     ClassAtomicity,
		Rule:      "one-transaction-commits-or-aborts-as-one-unit",
		Message:   fmt.Sprintf(format, args...),
		CommitLSN: commitLSN,
		TxID:      txID,
	}
}

func integrityError(recordID, txID, format string, args ...any) *ExportError {
	return &ExportError{
		Class:    ClassIntegrity,
		Rule:     "visible-links-require-visible-endpoints",
		Message:  fmt.Sprintf(format, args...),
		TxID:     txID,
		RecordID: recordID,
	}
}

func resourceError(format string, args ...any) *ExportError {
	return &ExportError{
		Class:   ClassResource,
		Rule:    "export-stops-before-resource-limit-is-overrun",
		Message: fmt.Sprintf(format, args...),
	}
}
