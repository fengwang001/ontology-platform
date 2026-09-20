package ontology

import "fmt"

// OpKind is the kind of a batch operation.
type OpKind int

const (
	// OpInsert inserts a new record.
	OpInsert OpKind = iota
	// OpDelete removes a record by primary key.
	OpDelete
)

// Op is one write operation inside a batch.
type Op struct {
	Kind  OpKind
	ID    string
	Props map[string]Value // OpInsert only
}

// InsertOp builds an insert operation.
func InsertOp(id string, props map[string]Value) Op {
	return Op{Kind: OpInsert, ID: id, Props: props}
}

// DeleteOp builds a delete operation.
func DeleteOp(id string) Op { return Op{Kind: OpDelete, ID: id} }

// Apply executes a batch atomically with deferred constraint
// checking: ops run in order against a staged copy, so a delete
// followed by an insert of an equivalent key succeeds, while two
// inserts of equivalent keys are rejected. Any conflict aborts
// the whole batch — the live store is left untouched — and the
// returned *BatchError identifies the failing op and, for
// batch-internal conflicts, the earlier op it collides with.
func (s *Store) Apply(ops ...Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := make(map[string]Record, len(s.records))
	for id, rec := range s.records {
		records[id] = rec
	}
	index := make(map[string]map[string]string, len(s.index))
	for name, m := range s.index {
		cm := make(map[string]string, len(m))
		for k, v := range m {
			cm[k] = v
		}
		index[name] = cm
	}

	// owner maps a primary key to the batch op that inserted it.
	owner := map[string]int{}
	for i, op := range ops {
		switch op.Kind {
		case OpInsert:
			if _, exists := records[op.ID]; exists {
				return fmt.Errorf("batch rejected: op %d: record %q already exists", i, op.ID)
			}
			if err := s.checkAll(op.ID, op.Props, records, index); err != nil {
				return &BatchError{Index: i, OtherIndex: ownerOr(err.ExistingID, owner), Err: err}
			}
			s.applyInsert(records, index, op.ID, op.Props)
			owner[op.ID] = i
		case OpDelete:
			s.applyDelete(records, index, op.ID)
			delete(owner, op.ID)
		default:
			return fmt.Errorf("batch rejected: op %d: unknown kind %d", i, op.Kind)
		}
	}

	s.records = records
	s.index = index
	return nil
}

// ownerOr returns the batch op index that inserted id, or -1 when
// the record pre-existed the batch.
func ownerOr(id string, owner map[string]int) int {
	if i, ok := owner[id]; ok {
		return i
	}
	return -1
}
