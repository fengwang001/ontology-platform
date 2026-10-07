package audit

import (
	"encoding/json"
	"sync"
)

// CallLogEntry is the complete record of one public Service call: its inputs,
// final output, error and the version/record basis used for adjudication.
type CallLogEntry struct {
	Seq       int64          `json:"seq"`
	Method    string         `json:"method"`
	Input     any            `json:"input"`
	Output    any            `json:"output,omitempty"`
	Error     string         `json:"error,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
	Basis     *DecisionBasis `json:"basis,omitempty"`
}

// DecisionBasis names the exact records and rule version a call relied on.
type DecisionBasis struct {
	RuleVersionID  string `json:"rule_version_id,omitempty"`
	RuleVersionSeq int64  `json:"rule_version_seq,omitempty"`
	AuditID        string `json:"audit_id,omitempty"`
	AuditSeq       int64  `json:"audit_seq,omitempty"`
	CorrectionID   string `json:"correction_id,omitempty"`
	CorrectionSeq  int64  `json:"correction_seq,omitempty"`
}

// Logger receives one entry per completed Service call.
type Logger interface {
	Log(entry CallLogEntry)
}

// SliceLogger keeps every entry in memory and is safe for concurrent use.
type SliceLogger struct {
	mu      sync.Mutex
	entries []CallLogEntry
}

// NewSliceLogger creates an empty SliceLogger.
func NewSliceLogger() *SliceLogger { return &SliceLogger{} }

// Log appends one entry.
func (l *SliceLogger) Log(entry CallLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.Seq = int64(len(l.entries)) + 1
	l.entries = append(l.entries, entry)
}

// Entries returns a copy of all logged entries.
func (l *SliceLogger) Entries() []CallLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]CallLogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// WriteJSONLines renders the whole log as JSON Lines.
func (l *SliceLogger) WriteJSONLines(w interface{ Write(p []byte) (int, error) }) error {
	for _, e := range l.Entries() {
		b, err := e.JSON()
		if err != nil {
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// JSON renders one entry canonically for log files and tests.
func (e CallLogEntry) JSON() ([]byte, error) { return json.Marshal(e) }
