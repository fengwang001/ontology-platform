package ontology

import (
	"encoding/json"
	"io"
	"sync"
)

// PolicyBasis records exactly which policies produced a verdict.
type PolicyBasis struct {
	MatchedRow      []string            `json:"matched_row"`
	MatchedProperty map[string][]string `json:"matched_property,omitempty"`
	RowMode         MergeMode           `json:"row_mode"`
	PropertyMode    MergeMode           `json:"property_mode"`
	RowVerdict      Effect              `json:"row_verdict"`
	Touched         int                 `json:"touched_policies"`
}

// AuditEntry is one JSONL record for one read or write call.
type AuditEntry struct {
	Seq     int          `json:"seq"`
	Op      string       `json:"op"`
	Subject string       `json:"subject"`
	Type    string       `json:"object_type"`
	ID      string       `json:"instance_id"`
	Input   any          `json:"input"`
	Output  any          `json:"output"`
	Error   string       `json:"error,omitempty"`
	Basis   *PolicyBasis `json:"basis,omitempty"`
}

// AuditLogger appends one record per adjudication.
type AuditLogger struct {
	mu  sync.Mutex
	seq int
	w   io.Writer
}

// NewAuditLogger creates a logger writing JSON lines to w. A nil w disables
// persistence while counters keep working.
func NewAuditLogger(w io.Writer) *AuditLogger { return &AuditLogger{w: w} }

// log appends one entry, assigning a stable monotonic sequence number. The
// record always contains the call input, the final output and the policy
// basis that produced the verdict.
func (l *AuditLogger) log(entry AuditEntry) AuditEntry {
	if l == nil {
		return entry
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	entry.Seq = l.seq
	if l.w != nil {
		data, err := json.Marshal(entry)
		if err == nil {
			_, _ = l.w.Write(append(data, '\n'))
		}
	}
	return entry
}

// Seq returns the number of entries logged so far.
func (l *AuditLogger) Seq() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.seq
}
