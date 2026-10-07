package ontology

import (
	"encoding/json"
	"io"
	"sync"
)

// Journal is the append-only, externally inspectable decision record.
// It is the replayable evidence required by the design: every batch's
// declared preconditions, the versions observed at the joint decision
// instant, and the final classified outcome are recorded in decision order.
type Journal struct {
	mu      sync.Mutex
	records []Record
}

// Record is one journal entry (creation or batch decision).
type Record struct {
	Kind    string  `json:"kind"`
	BatchID string  `json:"batch_id,omitempty"`
	Order   uint64  `json:"order,omitempty"`
	ID      ID      `json:"id,omitempty"`
	Type    string  `json:"type,omitempty"`
	Batch   *Batch  `json:"batch,omitempty"`
	Result  *Result `json:"result,omitempty"`
}

// NewJournal creates an empty journal.
func NewJournal() *Journal { return &Journal{records: []Record{}} }

// append records one decision under the journal lock.
func (j *Journal) append(r Record) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.records = append(j.records, r)
}

// appendLocked records one entry from a caller already serialised against
// the journal (store bootstrap paths).
func (j *Journal) appendLocked(r Record) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.records = append(j.records, r)
}

// Records returns a copy of all journal records.
func (j *Journal) Records() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Record, len(j.records))
	copy(out, j.records)
	return out
}

// WriteTo writes the journal as newline-delimited JSON.
func (j *Journal) WriteTo(w io.Writer) (int64, error) {
	j.mu.Lock()
	records := make([]Record, len(j.records))
	copy(records, j.records)
	j.mu.Unlock()

	var n int64
	for _, r := range records {
		raw, err := json.Marshal(r)
		if err != nil {
			return n, err
		}
		k, err := w.Write(append(raw, '\n'))
		n += int64(k)
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

var _ io.WriterTo = (*Journal)(nil)
