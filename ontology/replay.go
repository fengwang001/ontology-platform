package ontology

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ReplayReport summarises a journal replay.
type ReplayReport struct {
	Records   int           `json:"records"`
	Creates   int           `json:"creates"`
	Batches   int           `json:"batches"`
	Committed int           `json:"committed"`
	Rejected  int           `json:"rejected"`
	Versions  map[ID]uint64 `json:"versions"`
}

// ReplayJournal reads newline-delimited journal records from r, re-executes
// every decision against a fresh store built with cfg, and verifies that
// the re-derived result matches the recorded evidence bit-for-bit on the
// fields that matter (status, mismatches, cardinality, version ranges and
// observed versions). It returns the final report.
func ReplayJournal(cfg Config, r io.Reader) (ReplayReport, error) {
	store := New(cfg)
	report := ReplayReport{Versions: map[ID]uint64{}}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(raw, &rec); err != nil {
			return report, fmt.Errorf("line %d: decode: %w", line, err)
		}
		report.Records++
		switch rec.Kind {
		case "create":
			if err := store.CreateInstance(rec.ID, rec.Type); err != nil {
				return report, fmt.Errorf("line %d: create %s: %w", line, rec.ID, err)
			}
			report.Creates++
		case "batch":
			if rec.Batch == nil || rec.Result == nil {
				return report, fmt.Errorf("line %d: batch record missing payload", line)
			}
			report.Batches++
			got := store.Commit(*rec.Batch)
			if err := resultsEquivalent(got, *rec.Result); err != nil {
				return report, fmt.Errorf("line %d batch %s: %w", line, rec.Batch.ID, err)
			}
			if got.Status == StatusCommitted {
				report.Committed++
			} else {
				report.Rejected++
			}
		default:
			return report, fmt.Errorf("line %d: unknown record kind %q", line, rec.Kind)
		}
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}

	for id := range store.instances {
		it := store.instances[id]
		it.mu.Lock()
		report.Versions[id] = it.version
		it.mu.Unlock()
	}
	return report, nil
}

// resultsEquivalent compares a replayed decision with recorded evidence.
// Order is intentionally excluded: it is a runtime assignment, while the
// replay verifies causal decision content.
func resultsEquivalent(got, want Result) error {
	if got.Status != want.Status {
		return fmt.Errorf("status: got %s want %s", got.Status, want.Status)
	}
	if got.Duplicate != want.Duplicate {
		return fmt.Errorf("duplicate: got %q want %q", got.Duplicate, want.Duplicate)
	}
	if diffMismatches(got.Mismatches, want.Mismatches) {
		return fmt.Errorf("mismatch list differs: got %v want %v", got.Mismatches, want.Mismatches)
	}
	if diffCardinality(got.Cardinality, want.Cardinality) {
		return fmt.Errorf("cardinality list differs: got %v want %v", got.Cardinality, want.Cardinality)
	}
	if !sameVersionChanges(got.Versions, want.Versions) {
		return fmt.Errorf("version changes differ: got %v want %v", got.Versions, want.Versions)
	}
	if !sameObserved(got.Observed, want.Observed) {
		return fmt.Errorf("observed versions differ: got %v want %v", got.Observed, want.Observed)
	}
	return nil
}

func diffMismatches(a, b []Mismatch) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return true
		}
	}
	return false
}

func diffCardinality(a, b []CardinalityFailure) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return true
		}
	}
	return false
}

func sameVersionChanges(a, b map[ID]VersionChange) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sameObserved(a, b map[ID]uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
