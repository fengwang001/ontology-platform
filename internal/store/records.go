package store

import (
	"encoding/json"
	"fmt"
)

// On-disk value types. All records are versioned JSON blobs.

type instanceRecord struct {
	Kind       string            `json:"kind"`
	ID         string            `json:"id"`
	Version    int64             `json:"version"`
	Properties map[string]string `json:"properties"`
}

type intentRecord struct {
	Kind       string            `json:"kind"`
	BatchID    string            `json:"batch_id"`
	ObjectID   string            `json:"object_id"`
	Seq        int               `json:"seq"`
	OldVersion int64             `json:"old_version"`
	NewVersion int64             `json:"new_version"`
	OldProps   map[string]string `json:"old_props"`
	NewProps   map[string]string `json:"new_props"`
}

type stateRecord struct {
	Kind    string `json:"kind"`
	BatchID string `json:"batch_id"`
	State   string `json:"state"`
	Count   int    `json:"count"`
}

type journalRecord struct {
	Seq     int    `json:"seq"`
	Phase   string `json:"phase"`
	BatchID string `json:"batch_id"`
	Detail  string `json:"detail"`
}

func encode(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("store: encode: %w", err)
	}
	return b, nil
}

func decode[T any](b []byte) (T, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("store: decode: %w", err)
	}
	return v, nil
}

// Key layout (hex encoding keeps filenames portable for FileEngine):
//
//	i/<hex id>          current value of one instance
//	b/<hex id>/active   batch state machine marker
//	b/<hex id>/i/<seq>  one prepared intent
//	active               id of the single in-flight batch (pointer only)
//	j/<seq>              one append-only audit journal entry
const (
	keyActiveBatch = "active"
)

func keyJournalEntry(seq int) string { return fmt.Sprintf("j/%010d", seq) }

func keyInstance(id string) string {
	return "i/" + hexID(id)
}

func keyBatchState(batchID string) string {
	return "b/" + hexID(batchID) + "/active"
}

func keyIntent(batchID string, seq int) string {
	return fmt.Sprintf("b/%s/i/%06d", hexID(batchID), seq)
}

func hexID(id string) string {
	return fmt.Sprintf("%x", id)
}
