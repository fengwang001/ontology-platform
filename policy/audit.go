package policy

// AuditLog records committed presentation requests only.

import (
	"encoding/json"
	"sync"
)

// AuditEntry is the complete, reproducible record of one committed render:
// its inputs, its final output, and the exact policy basis per attribute.
type AuditEntry struct {
	Revision        int64                  `json:"revision"`
	ObjectType      string                 `json:"object_type"`
	InstanceID      string                 `json:"instance_id"`
	Subject         string                 `json:"subject"`
	RawInstance     map[string]any         `json:"raw_instance"`
	Presented       map[string]any         `json:"presented"`
	AttributeErrors []AttributeErrorRecord `json:"attribute_errors,omitempty"`
	Basis           map[string]AttrBasis   `json:"basis"`
}

// AttributeErrorRecord is the JSON-friendly view of a per-attribute error.
type AttributeErrorRecord struct {
	Attr string `json:"attr"`
	Kind string `json:"kind"`
	Msg  string `json:"msg"`
}

// AuditLog is an append-only, concurrency-safe in-memory record. Denied
// requests (phase 1-3 failures) never call Append, so they leave no trace.
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// NewAuditLog creates an empty audit log.
func NewAuditLog() *AuditLog { return &AuditLog{} }

// Append records one committed result. Raw values are copied to defend
// against later caller mutation.
func (l *AuditLog) Append(inst Instance, subject string, res *Result) {
	raw := make(map[string]any, len(inst.Values))
	for k, v := range inst.Values {
		raw[k] = v
	}
	errs := make([]AttributeErrorRecord, 0, len(res.AttributeErrors))
	for _, ae := range res.AttributeErrors {
		errs = append(errs, AttributeErrorRecord{Attr: ae.Attr, Kind: ae.Err.Kind.String(), Msg: ae.Err.Msg})
	}
	basis := make(map[string]AttrBasis, len(res.Basis))
	for k, v := range res.Basis {
		basis[k] = v
	}
	entry := AuditEntry{
		Revision:        res.Revision,
		ObjectType:      res.Object,
		InstanceID:      res.Instance,
		Subject:         subject,
		RawInstance:     raw,
		Presented:       res.Presented,
		AttributeErrors: errs,
		Basis:           basis,
	}
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.mu.Unlock()
}

// Entries returns a deep-copied snapshot of all recorded entries.
func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Len returns the number of committed renders recorded.
func (l *AuditLog) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// JSON renders the full log for inspection / compliance tooling.
func (l *AuditLog) JSON() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return json.MarshalIndent(l.entries, "", "  ")
}
