// Package idempotent 提供按分区与位点去重的幂等落库组件。
package idempotent

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
)

// Record 是批次中的一条记录：属于某分区的某位点，按 key 累加到结果表。
type Record struct {
	Partition int
	Offset    int64
	Key       string
	Amount    int64
}

// BatchResult 是一个批次提交后的判定结果。
type BatchResult struct {
	Applied        int
	Duplicate      int
	AdvancedWater  int
	Watermarks     map[int]int64
	DuplicateTotal int64
	Results        map[string]int64
}

// Config 控制 Sink 的容量与日志。
type Config struct {
	MaxPartitions int
	Logger        *slog.Logger
}

// snapshot 是需要整体原子持久化的状态。
type snapshot struct {
	Version    int              `json:"version"`
	Watermarks map[int]int64    `json:"watermarks"`
	Duplicates map[int]int64    `json:"duplicates"`
	Results    map[string]int64 `json:"results"`
}

// StateStore 负责状态快照的原子读写。
type StateStore interface {
	Load(ctx context.Context) (*snapshot, error)
	Commit(ctx context.Context, snap *snapshot) error
}

// Sink 按分区独立位点水位去重，并把生效记录幂等累加到结果表。
type Sink struct {
	mu      sync.Mutex
	store   StateStore
	logger  *slog.Logger
	maxPart int

	watermarks map[int]int64
	duplicates map[int]int64
	results    map[string]int64
}

// NewSink 从 StateStore 重建状态；无历史状态时从空状态启动。
func NewSink(store StateStore, cfg Config) (*Sink, error) {
	if store == nil {
		return nil, errors.New("idempotent: state store must not be nil")
	}
	maxPart := cfg.MaxPartitions
	if maxPart <= 0 {
		maxPart = 1024
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Sink{
		store:      store,
		logger:     logger,
		maxPart:    maxPart,
		watermarks: map[int]int64{},
		duplicates: map[int]int64{},
		results:    map[string]int64{},
	}
	snap, err := store.Load(context.Background())
	if err != nil {
		return nil, err
	}
	if snap != nil {
		for p, w := range snap.Watermarks {
			s.watermarks[p] = w
		}
		for p, d := range snap.Duplicates {
			s.duplicates[p] = d
		}
		for k, v := range snap.Results {
			s.results[k] = v
		}
	}
	logger.Info("sink restored from persisted state",
		slog.Int("partitions", len(s.watermarks)),
		slog.Int("result_keys", len(s.results)))
	return s, nil
}

// Write 原子地写入一个批次：先校验，再按水位判重，最后整体提交。
func (s *Sink) Write(ctx context.Context, batch []Record) (BatchResult, error) {
	s.logger.Log(ctx, slog.LevelDebug, "write batch input",
		slog.Int("records", len(batch)))

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validate(batch); err != nil {
		s.logger.Warn("batch rejected before any effect",
			slog.String("reason", err.Error()),
			slog.Int("records", len(batch)))
		return BatchResult{}, err
	}

	if len(batch) == 0 {
		return s.result(0, 0, 0), nil
	}

	nextWater := cloneInt64Map(s.watermarks)
	nextDup := cloneInt64Map(s.duplicates)
	nextResults := cloneStringMap(s.results)

	applied, duplicate, advanced := 0, 0, 0
	for i, rec := range batch {
		water, known := nextWater[rec.Partition]
		if known && rec.Offset <= water {
			duplicate++
			nextDup[rec.Partition]++
			s.logger.Log(ctx, slog.LevelDebug, "record judged duplicate",
				slog.Int("index", i),
				slog.Int("partition", rec.Partition),
				slog.Int64("offset", rec.Offset),
				slog.Int64("watermark", water),
				slog.String("basis", "offset<=watermark"))
		} else {
			nextResults[rec.Key] += rec.Amount
			nextWater[rec.Partition] = rec.Offset
			applied++
			advanced++
			s.logger.Log(ctx, slog.LevelDebug, "record judged applied",
				slog.Int("index", i),
				slog.Int("partition", rec.Partition),
				slog.Int64("offset", rec.Offset),
				slog.Int64("prev_watermark", water),
				slog.String("key", rec.Key),
				slog.Int64("amount", rec.Amount),
				slog.String("basis", "offset>watermark"))
		}
	}

	snap := &snapshot{
		Version:    1,
		Watermarks: nextWater,
		Duplicates: nextDup,
		Results:    nextResults,
	}
	if err := s.store.Commit(ctx, snap); err != nil {
		s.logger.Error("commit failed; in-memory state unchanged",
			slog.Any("error", err))
		return BatchResult{}, err
	}

	s.watermarks = nextWater
	s.duplicates = nextDup
	s.results = nextResults

	res := s.result(applied, duplicate, advanced)
	s.logger.Info("batch committed atomically",
		slog.Int("records", len(batch)),
		slog.Int("applied", applied),
		slog.Int("duplicate", duplicate),
		slog.Int("watermarks_advanced", advanced),
		slog.Int64("duplicate_total", res.DuplicateTotal),
		slog.String("basis", "whole snapshot committed or rejected as one unit"))
	return res, nil
}

// Snapshot 返回当前持久状态的只读副本。
func (s *Sink) Snapshot() (watermarks, duplicates map[int]int64, results map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneInt64Map(s.watermarks), cloneInt64Map(s.duplicates), cloneStringMap(s.results)
}

func (s *Sink) validate(batch []Record) error {
	seen := make(map[int]int64, len(batch))
	touched := make(map[int]struct{}, len(batch))
	for i, rec := range batch {
		if rec.Partition < 0 {
			return reject(ErrNegativePartition, i,
				"partition must be non-negative, got %d", rec.Partition)
		}
		if rec.Offset < 0 {
			return reject(ErrNegativeOffset, i,
				"offset must be non-negative, got %d", rec.Offset)
		}
		if strings.TrimSpace(rec.Key) == "" {
			return reject(ErrEmptyKey, i, "record key must not be empty")
		}
		if last, ok := seen[rec.Partition]; ok && rec.Offset <= last {
			return reject(ErrOutOfOrderOffset, i,
				"offsets in partition %d must be strictly increasing within a batch: %d follows %d",
				rec.Partition, rec.Offset, last)
		}
		seen[rec.Partition] = rec.Offset
		touched[rec.Partition] = struct{}{}
	}
	if len(touched) > 0 {
		newPartitions := 0
		for p := range touched {
			if _, known := s.watermarks[p]; !known {
				newPartitions++
			}
		}
		total := len(s.watermarks) + newPartitions
		if total > s.maxPart {
			return reject(ErrTooManyPartitions, -1,
				"partition count %d exceeds limit %d", total, s.maxPart)
		}
	}
	return nil
}

func (s *Sink) result(applied, duplicate, advanced int) BatchResult {
	var dupTotal int64
	for _, n := range s.duplicates {
		dupTotal += n
	}
	return BatchResult{
		Applied:        applied,
		Duplicate:      duplicate,
		AdvancedWater:  advanced,
		Watermarks:     cloneInt64Map(s.watermarks),
		DuplicateTotal: dupTotal,
		Results:        cloneStringMap(s.results),
	}
}

func cloneInt64Map(in map[int]int64) map[int]int64 {
	out := make(map[int]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringMap(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
