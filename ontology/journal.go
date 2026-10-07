package ontology

import (
	"encoding/json"
	"os"
	"sync"
)

// JournalEntry is a complete, replayable record of one commit attempt.
//
// Every attempt is recorded — successes and all failure classes — so that the
// full history can be replayed offline against an independent
// implementation. A failed entry carries Tick 0: failed batches never advance
// logical time and never mutate versions, links or clock state.
type JournalEntry struct {
	Seq       int64            `json:"seq"`
	BatchID   string           `json:"batch_id"`
	Batch     BatchRecord      `json:"batch"`
	Base      map[string]State `json:"base"` // versions/props of locked instances as observed
	OK        bool             `json:"ok"`
	Failure   string           `json:"failure,omitempty"`
	Detail    string           `json:"detail,omitempty"`
	Tick      int64            `json:"tick,omitempty"`
	HookNotes []string         `json:"hook_notes,omitempty"`
	NewState  map[string]State `json:"new_state,omitempty"` // only changed instances
	NewLinks  []LinkRecord     `json:"new_links,omitempty"` // full final link set (bounded test helper)
}

// State is the serializable state of one instance.
type State struct {
	Type    string                 `json:"type,omitempty"`
	Version int64                  `json:"version"`
	Exists  bool                   `json:"exists"`
	Props   map[string]interface{} `json:"props,omitempty"`
}

// BatchRecord is the serializable form of a Batch.
type BatchRecord struct {
	Ops   []OpRecord   `json:"ops"`
	Links []LinkRecord `json:"links"`
}

// OpRecord is one instance operation as recorded.
type OpRecord struct {
	Instance    string                 `json:"instance"`
	Type        string                 `json:"type"`
	BaseVersion int64                  `json:"base_version"`
	Props       map[string]interface{} `json:"props,omitempty"`
	Delete      bool                   `json:"delete,omitempty"`
}

// LinkRecord is one link, or one link op.
type LinkRecord struct {
	Link string `json:"link"`
	A    string `json:"a"`
	B    string `json:"b"`
	Add  bool   `json:"add,omitempty"`
}

// Journal is an in-memory append log of every commit decision.
type Journal struct {
	mu      sync.Mutex
	entries []JournalEntry
	sink    string // optional file appended on each entry
}

// NewJournal creates a journal. If sinkPath is non-empty, each entry is also
// appended as a JSON line to that file.
func NewJournal(sinkPath string) *Journal {
	return &Journal{sink: sinkPath}
}

// Append records one entry.
func (j *Journal) Append(e JournalEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.sink != "" {
		if data, err := json.Marshal(e); err == nil {
			f, err := os.OpenFile(j.sink, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err == nil {
				f.Write(append(data, '\n'))
				f.Close()
			}
		}
	}
	j.entries = append(j.entries, e)
}

// Entries returns a copy of all recorded entries.
func (j *Journal) Entries() []JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]JournalEntry, len(j.entries))
	copy(out, j.entries)
	return out
}

func failureName(c FailureClass) string {
	switch c {
	case FailureDuplicateWrite:
		return "duplicate_write"
	case FailureVersionConflict:
		return "version_conflict"
	case FailureHookRejected:
		return "hook_rejected"
	case FailureCardinality:
		return "cardinality_violation"
	default:
		return ""
	}
}

func recordBatch(b Batch) BatchRecord {
	rec := BatchRecord{
		Ops:   make([]OpRecord, 0, len(b.Ops)),
		Links: make([]LinkRecord, 0, len(b.Links)),
	}
	for _, op := range b.Ops {
		rec.Ops = append(rec.Ops, OpRecord{
			Instance:    string(op.Instance),
			Type:        string(op.Type),
			BaseVersion: op.BaseVersion,
			Props:       propsToJSON(op.Props),
			Delete:      op.Props == nil,
		})
	}
	for _, l := range b.Links {
		rec.Links = append(rec.Links, LinkRecord{Link: string(l.Link), A: string(l.A), B: string(l.B), Add: l.Add})
	}
	return rec
}

func propsToJSON(in Properties) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
