// Package dedup provides an idempotent sink that deduplicates deliveries
// by partition and offset.
//
// Each partition independently tracks the highest offset that has taken
// effect (its watermark). Records with offset <= watermark are duplicates
// and are dropped; records above the watermark are accumulated into the
// result table and advance the watermark. The result table, all partition
// watermarks and the duplicate counter are committed atomically and
// persisted, so after a crash the sink rebuilds only from persisted state.
package dedup

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Record is a single record in a batch.
type Record struct {
	Partition int
	Offset    int64
	Key       string
	Value     int64
}

// Batch is one delivery; it is applied atomically or rejected as a whole.
type Batch struct {
	Records []Record
}

// ApplyResult describes the outcome of applying a batch.
type ApplyResult struct {
	Applied    int
	Duplicates int
}

// Snapshot is a consistent view of the persisted state.
type Snapshot struct {
	Results    map[string]int64
	Watermarks []int64
	Duplicates int64
}

// Sink is the idempotent sink. It is safe for concurrent use.
type Sink struct {
	mu         sync.Mutex
	maxParts   int
	results    map[string]int64
	watermarks []int64
	duplicates int64
	statePath  string
	logger     *slog.Logger
}

// persistedState is the on-disk format of the sink state.
type persistedState struct {
	Results    map[string]int64 `json:"results"`
	Watermarks []int64          `json:"watermarks"`
	Duplicates int64            `json:"duplicates"`
}

// Open opens (or creates) a Sink persisting to statePath.
// If persisted state exists it is the only source used to rebuild.
func Open(statePath string, maxPartitions int) (*Sink, error) {
	if maxPartitions <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidPartitionCount, maxPartitions)
	}
	s := &Sink{
		maxParts:   maxPartitions,
		results:    map[string]int64{},
		watermarks: make([]int64, maxPartitions),
		statePath:  statePath,
		logger:     slog.Default(),
	}
	for i := range s.watermarks {
		s.watermarks[i] = -1
	}
	data, err := os.ReadFile(statePath)
	switch {
	case err == nil:
		var ps persistedState
		if err := json.Unmarshal(data, &ps); err != nil {
			return nil, fmt.Errorf("dedup: corrupt state file %s: %w", statePath, err)
		}
		if len(ps.Watermarks) != maxPartitions {
			return nil, fmt.Errorf("dedup: state file has %d partitions, want %d", len(ps.Watermarks), maxPartitions)
		}
		if ps.Results == nil {
			ps.Results = map[string]int64{}
		}
		s.results = ps.Results
		s.watermarks = ps.Watermarks
		s.duplicates = ps.Duplicates
		s.logger.Info("sink recovered from persisted state",
			"path", statePath, "watermarks", s.watermarks, "duplicates", s.duplicates)
	case os.IsNotExist(err):
		s.logger.Info("sink initialized with empty state", "path", statePath, "partitions", maxPartitions)
	default:
		return nil, fmt.Errorf("dedup: read state file: %w", err)
	}
	return s, nil
}

// Apply validates and atomically applies a batch.
// On rejection nothing changes: results, watermarks and the duplicate
// counter are left untouched.
func (s *Sink) Apply(b Batch) (ApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("batch received", "records", len(b.Records))
	if err := s.validate(b); err != nil {
		s.logger.Warn("batch rejected", "reason", err)
		return ApplyResult{}, err
	}

	// Compute the next state on copies; only swap in after persistence
	// succeeds, so a failed commit leaves the previous state intact.
	results := make(map[string]int64, len(s.results)+len(b.Records))
	for k, v := range s.results {
		results[k] = v
	}
	watermarks := make([]int64, len(s.watermarks))
	copy(watermarks, s.watermarks)

	var res ApplyResult
	for i, r := range b.Records {
		wm := watermarks[r.Partition]
		if r.Offset <= wm {
			res.Duplicates++
			s.logger.Info("record duplicate, dropped",
				"index", i, "partition", r.Partition, "offset", r.Offset,
				"key", r.Key, "watermark", wm,
				"reason", "offset <= partition watermark")
			continue
		}
		results[r.Key] += r.Value
		watermarks[r.Partition] = r.Offset
		res.Applied++
		s.logger.Info("record applied",
			"index", i, "partition", r.Partition, "offset", r.Offset,
			"key", r.Key, "value", r.Value,
			"reason", fmt.Sprintf("offset %d > watermark %d", r.Offset, wm))
	}

	duplicates := s.duplicates + int64(res.Duplicates)
	if err := persist(s.statePath, persistedState{
		Results:    results,
		Watermarks: watermarks,
		Duplicates: duplicates,
	}); err != nil {
		return ApplyResult{}, fmt.Errorf("dedup: commit batch: %w", err)
	}
	s.results = results
	s.watermarks = watermarks
	s.duplicates = duplicates
	s.logger.Info("batch committed",
		"applied", res.Applied, "duplicates", res.Duplicates,
		"watermarks", watermarks, "totalDuplicates", duplicates)
	return res, nil
}

// validate checks the whole batch; any violation rejects the batch.
func (s *Sink) validate(b Batch) error {
	last := make(map[int]int64, len(b.Records))
	seen := make(map[int]bool, len(b.Records))
	for i, r := range b.Records {
		switch {
		case r.Partition < 0:
			return fmt.Errorf("%w: record %d partition %d", ErrNegativePartition, i, r.Partition)
		case r.Partition >= s.maxParts:
			return fmt.Errorf("%w: record %d partition %d >= %d", ErrPartitionLimit, i, r.Partition, s.maxParts)
		case r.Offset < 0:
			return fmt.Errorf("%w: record %d offset %d", ErrNegativeOffset, i, r.Offset)
		case r.Key == "":
			return fmt.Errorf("%w: record %d partition %d offset %d", ErrEmptyKey, i, r.Partition, r.Offset)
		}
		if seen[r.Partition] && r.Offset <= last[r.Partition] {
			return fmt.Errorf("%w: record %d partition %d offset %d after %d",
				ErrOutOfOrder, i, r.Partition, r.Offset, last[r.Partition])
		}
		seen[r.Partition] = true
		last[r.Partition] = r.Offset
	}
	return nil
}

// persist writes the state atomically: temp file, fsync, rename, fsync dir.
func persist(path string, st persistedState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}

// Snapshot returns a consistent snapshot of the current state.
func (s *Sink) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make(map[string]int64, len(s.results))
	for k, v := range s.results {
		results[k] = v
	}
	watermarks := make([]int64, len(s.watermarks))
	copy(watermarks, s.watermarks)
	return Snapshot{Results: results, Watermarks: watermarks, Duplicates: s.duplicates}
}

// Close closes the Sink.
func (s *Sink) Close() error {
	return nil
}
