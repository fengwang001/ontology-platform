package idempotent

import (
	"fmt"
	"log/slog"
	"sync"
)

// DefaultMaxPartitions 是默认的分区数上限。
const DefaultMaxPartitions = 1024

// Sink 按分区与位点去重的幂等落库组件。
//
// 每个分区独立维护已生效的最大位点（水位）：位点不超过水位的记录视为
// 重复并丢弃，超过水位的记录累加到结果表并推进水位。结果表、各分区水位
// 与累计重复数在每次提交时作为一个整体原子落盘，崩溃重启后仅从磁盘状态
// 重建，因此至少一次投递不会导致漏计或重复计。
type Sink struct {
	mu            sync.Mutex
	path          string
	maxPartitions int
	log           *slog.Logger
	state         persistedState
}

// Config 用于打开 Sink。
type Config struct {
	// Path 是状态文件路径，必填。
	Path string
	// MaxPartitions 是允许出现的最大分区数，<=0 时使用 DefaultMaxPartitions。
	MaxPartitions int
	// Logger 用于记录输入、生效/重复判定及依据，为 nil 时使用 slog.Default()。
	Logger *slog.Logger
}

// Open 打开（或重建）一个 Sink。状态文件不存在时从空状态启动，
// 存在时仅以磁盘上的持久状态为准重建。
func Open(cfg Config) (*Sink, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("idempotent: state path is required")
	}
	maxPartitions := cfg.MaxPartitions
	if maxPartitions <= 0 {
		maxPartitions = DefaultMaxPartitions
	}
	state, err := loadState(cfg.Path)
	if err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Sink{
		path:          cfg.Path,
		maxPartitions: maxPartitions,
		log:           logger,
		state:         state,
	}
	s.log.Info("sink opened, state rebuilt from persistence",
		slog.String("path", cfg.Path),
		slog.Int("partitions", len(state.Watermarks)),
		slog.Int64("duplicates", state.Duplicates))
	return s, nil
}

// Apply 并发安全地提交一批记录。
//
// 批次先整体校验（负分区/负位点/空键/同批同分区位点非严格递增/分区数超限），
// 任一记录非法则整批拒绝，状态不变。校验通过后逐记录判定：位点不超过该
// 分区水位的记为重复并丢弃，其余累加到结果表并推进水位；随后把结果表、
// 全部水位与累计重复数作为整体原子落盘，落盘成功才在内存生效。
func (s *Sink) Apply(batch Batch) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Info("batch received",
		slog.String("batch_id", batch.ID),
		slog.Int("records", len(batch.Records)))

	if err := s.validate(batch); err != nil {
		s.log.Warn("batch rejected, no state changed",
			slog.String("batch_id", batch.ID),
			slog.String("reason", err.Error()))
		return Outcome{Rejected: true, Err: err}
	}

	candidate := s.state.clone()
	applied, duplicate := 0, 0
	for i, rec := range batch.Records {
		watermark, seen := candidate.Watermarks[rec.Partition]
		if seen && rec.Offset <= watermark {
			duplicate++
			candidate.Duplicates++
			s.log.Info("record duplicate, skipped",
				slog.String("batch_id", batch.ID),
				slog.Int("index", i),
				slog.Int("partition", rec.Partition),
				slog.Int64("offset", rec.Offset),
				slog.Int64("watermark", watermark),
				slog.String("key", rec.Key),
				slog.String("reason", fmt.Sprintf("partition seen and offset %d <= watermark %d", rec.Offset, watermark)))
			continue
		}
		candidate.Results[rec.Key] += rec.Value
		candidate.Watermarks[rec.Partition] = rec.Offset
		applied++
		s.log.Info("record applied",
			slog.String("batch_id", batch.ID),
			slog.Int("index", i),
			slog.Int("partition", rec.Partition),
			slog.Int64("offset", rec.Offset),
			slog.Int64("prev_watermark", watermark),
			slog.Int64("new_watermark", rec.Offset),
			slog.String("key", rec.Key),
			slog.Int64("value", rec.Value),
			slog.String("reason", fmt.Sprintf("offset %d > watermark %d", rec.Offset, watermark)))
	}

	if err := saveState(s.path, candidate); err != nil {
		s.log.Error("atomic commit failed, in-memory state kept unchanged",
			slog.String("batch_id", batch.ID),
			slog.String("error", err.Error()))
		return Outcome{Err: err}
	}
	s.state = candidate

	s.log.Info("batch committed atomically",
		slog.String("batch_id", batch.ID),
		slog.Int("applied", applied),
		slog.Int("duplicate", duplicate),
		slog.Int64("total_duplicates", candidate.Duplicates))
	return Outcome{Applied: applied, Duplicate: duplicate}
}

// validate 对整批做校验，返回首个可区分原因的错误。
func (s *Sink) validate(batch Batch) error {
	lastInBatch := make(map[int]int64)
	newPartitions := make(map[int]struct{})

	for i, rec := range batch.Records {
		if rec.Partition < 0 {
			return fmt.Errorf("%w: index %d has partition %d", ErrNegativePartition, i, rec.Partition)
		}
		if rec.Offset < 0 {
			return fmt.Errorf("%w: index %d partition %d has offset %d",
				ErrNegativeOffset, i, rec.Partition, rec.Offset)
		}
		if rec.Key == "" {
			return fmt.Errorf("%w: index %d partition %d offset %d",
				ErrEmptyKey, i, rec.Partition, rec.Offset)
		}
		if prev, ok := lastInBatch[rec.Partition]; ok && rec.Offset <= prev {
			return fmt.Errorf("%w: partition %d offset %d follows %d",
				ErrOutOfOrder, rec.Partition, rec.Offset, prev)
		}
		lastInBatch[rec.Partition] = rec.Offset
		if _, known := s.state.Watermarks[rec.Partition]; !known {
			newPartitions[rec.Partition] = struct{}{}
		}
	}

	if len(s.state.Watermarks)+len(newPartitions) > s.maxPartitions {
		return fmt.Errorf("%w: current %d + new %d > limit %d",
			ErrTooManyPartitions, len(s.state.Watermarks), len(newPartitions), s.maxPartitions)
	}
	return nil
}

// Snapshot 返回当前已提交状态的深拷贝。
func (s *Sink) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make(map[string]int64, len(s.state.Results))
	for k, v := range s.state.Results {
		results[k] = v
	}
	watermarks := make(map[int]int64, len(s.state.Watermarks))
	for p, v := range s.state.Watermarks {
		watermarks[p] = v
	}
	return Snapshot{
		Results:       results,
		Watermarks:    watermarks,
		Duplicates:    s.state.Duplicates,
		MaxPartitions: s.maxPartitions,
	}
}

func (st persistedState) clone() persistedState {
	results := make(map[string]int64, len(st.Results)+1)
	for k, v := range st.Results {
		results[k] = v
	}
	watermarks := make(map[int]int64, len(st.Watermarks)+1)
	for p, v := range st.Watermarks {
		watermarks[p] = v
	}
	return persistedState{
		Results:    results,
		Watermarks: watermarks,
		Duplicates: st.Duplicates,
	}
}
