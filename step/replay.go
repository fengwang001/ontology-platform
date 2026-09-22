package step

import "ontology/journal"

// Table is the folded state of every step plus global journal markers.
type Table struct {
	Steps        map[string]Status
	// Compensating is true after the global compensating marker record.
	Compensating bool
	// Terminal holds the global terminal phase from the journal, or "".
	Terminal     journal.Phase
}

// NewTable creates an empty table for the given step IDs in Pending state.
func NewTable(ids []string) *Table {
	t := &Table{Steps: make(map[string]Status, len(ids))}
	for _, id := range ids {
		t.Steps[id] = Status{State: Pending}
	}
	return t
}

// Get returns the status of id; unknown IDs get a zero-value Status.
func (t *Table) Get(id string) Status {
	if s, ok := t.Steps[id]; ok {
		return s
	}
	return Status{}
}

// Apply folds one record into the table.
func (t *Table) Apply(r journal.Record) {
	switch r.Phase {
	case journal.Compensating:
		t.Compensating = true
	case journal.AllCompleted:
		t.Terminal = journal.AllCompleted
	case journal.AllCompensated:
		t.Terminal = journal.AllCompensated
	default:
		s := t.Steps[r.StepID]
		t.Steps[r.StepID] = Fold(s, r)
	}
}
