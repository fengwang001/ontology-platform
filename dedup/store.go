package dedup

import (
	"log/slog"
	"sync"
)

// DefaultMaxPartitions 是允许出现的不同分区总数的默认上限。
const DefaultMaxPartitions = 4096

// Store 是按分区位点去重的幂等落库组件。
// 每次 Apply 原子地完成：整批校验 -> 按分区水位判定重复 -> 累加结果表并推进水位 -> 整体持久化。
type Store struct {
	mu         sync.Mutex
	path       string
	maxParts   int
	logger     *slog.Logger
	totals     map[string]int64
	partitions map[int]PartitionState
}

// Option 配置 Store。
type Option func(*config)

type config struct {
	path          string
	maxPartitions int
	logger        *slog.Logger
}

// WithPath 指定持久化快照文件路径；为空则只在内存中保存状态。
func WithPath(path string) Option {
	return func(c *config) { c.path = path }
}

// WithMaxPartitions 限制允许出现的不同分区总数（含已知分区），0 表示使用默认值。
func WithMaxPartitions(n int) Option {
	return func(c *config) { c.maxPartitions = n }
}

// WithLogger 注入日志记录器；默认使用 slog.Default()。
func WithLogger(logger *slog.Logger) Option {
	return func(c *config) { c.logger = logger }
}

// New 创建 Store；若 path 指向已有快照，则仅从持久状态重建内存状态。
func New(opts ...Option) (*Store, error) {
	cfg := config{maxPartitions: DefaultMaxPartitions, logger: slog.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxPartitions <= 0 {
		cfg.maxPartitions = DefaultMaxPartitions
	}
	totals, partitions, err := loadSnapshot(cfg.path)
	if err != nil {
		return nil, err
	}
	s := &Store{
		path:       cfg.path,
		maxParts:   cfg.maxPartitions,
		logger:     cfg.logger,
		totals:     totals,
		partitions: partitions,
	}
	s.logger.Info("store ready, state rebuilt from persistence",
		slog.String("path", cfg.path),
		slog.Int("keys", len(totals)),
		slog.Int("partitions", len(partitions)))
	return s, nil
}

// Apply 原子地提交一个批次。被拒绝时返回 *RejectError，且结果表、水位、重复数均不改变。
func (s *Store) Apply(records []Record) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("batch received", slog.Int("size", len(records)))
	if err := s.validate(records); err != nil {
		s.logger.Warn("batch rejected, no state changed",
			slog.String("reason", string(err.Reason)),
			slog.Int("index", err.Index),
			slog.Int("partition", err.Partition),
			slog.String("detail", err.Message))
		return zeroResult(), err
	}

	// 全部计算都在副本上进行；只有持久化成功后才替换正式状态，
	// 因此持久化失败或中途 panic 都不会污染内存状态。
	nextTotals := cloneTotals(s.totals)
	nextParts := clonePartitions(s.partitions)
	res := Result{Totals: map[string]int64{}, Watermarks: map[int]int64{}}

	for i, r := range records {
		ps := nextParts[r.Partition]
		if r.Offset <= ps.Watermark {
			ps.Duplicates++
			nextParts[r.Partition] = ps
			res.Duplicates = append(res.Duplicates, r)
			s.logger.Info("record judged duplicate, dropped",
				slog.Int("index", i),
				slog.Int("partition", r.Partition),
				slog.Int64("offset", r.Offset),
				slog.Int64("watermark", ps.Watermark),
				slog.String("basis", "offset <= partition watermark"))
			continue
		}
		nextTotals[r.Key] += r.Value
		ps.Watermark = r.Offset
		nextParts[r.Partition] = ps
		res.Applied = append(res.Applied, r)
		s.logger.Info("record judged effective, accumulated",
			slog.Int("index", i),
			slog.Int("partition", r.Partition),
			slog.Int64("offset", r.Offset),
			slog.String("key", r.Key),
			slog.Int64("value", r.Value),
			slog.String("basis", "offset > partition watermark"))
	}

	if err := persistSnapshot(s.path, nextTotals, nextParts); err != nil {
		s.logger.Error("persist failed, commit aborted, no state changed", slog.String("err", err.Error()))
		return zeroResult(), err
	}

	s.totals = nextTotals
	s.partitions = nextParts
	for key, value := range s.totals {
		res.Totals[key] = value
	}
	for partition, ps := range s.partitions {
		res.Watermarks[partition] = ps.Watermark
	}
	s.logger.Info("batch committed atomically",
		slog.Int("applied", len(res.Applied)),
		slog.Int("duplicates", len(res.Duplicates)))
	return res, nil
}

// validate 校验整批；任一记录非法即整批拒绝，拒绝时不触碰任何状态。
func (s *Store) validate(records []Record) *RejectError {
	if len(records) == 0 {
		return reject(ReasonEmptyBatch, -1, -1, "batch must contain at least one record")
	}
	lastOffset := make(map[int]int64, len(records))
	newPartitions := map[int]struct{}{}
	for i, r := range records {
		if r.Partition < 0 {
			return reject(ReasonNegativePartition, i, r.Partition, "partition %d is negative", r.Partition)
		}
		if r.Offset < 0 {
			return reject(ReasonNegativeOffset, i, r.Partition, "offset %d is negative", r.Offset)
		}
		if r.Key == "" {
			return reject(ReasonEmptyKey, i, r.Partition, "record key must not be empty")
		}
		if prev, ok := lastOffset[r.Partition]; ok {
			if r.Offset <= prev {
				return reject(ReasonOffsetNotIncreasing, i, r.Partition,
					"offset %d must be strictly greater than previous offset %d within same batch",
					r.Offset, prev)
			}
		}
		lastOffset[r.Partition] = r.Offset
		if _, known := s.partitions[r.Partition]; !known {
			newPartitions[r.Partition] = struct{}{}
		}
	}
	if len(s.partitions)+len(newPartitions) > s.maxParts {
		return reject(ReasonTooManyPartitions, -1, -1,
			"partition count %d exceeds limit %d", len(s.partitions)+len(newPartitions), s.maxParts)
	}
	return nil
}

// Total 返回某键当前的累计值。
func (s *Store) Total(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totals[key]
}

// Watermark 返回某分区已生效的最大位点；未知分区返回 -1。
func (s *Store) Watermark(partition int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps, ok := s.partitions[partition]; ok {
		return ps.Watermark

	}
	return -1
}

// Duplicates 返回某分区累计被判定为重复的条数。
func (s *Store) Duplicates(partition int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partitions[partition].Duplicates
}

// Snapshot 返回结果表与各分区状态的拷贝。
func (s *Store) Snapshot() (map[string]int64, map[int]PartitionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTotals(s.totals), clonePartitions(s.partitions)
}

// Close 释放资源。
func (s *Store) Close() error { return nil }

func cloneTotals(src map[string]int64) map[string]int64 {
	dst := make(map[string]int64, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func clonePartitions(src map[int]PartitionState) map[int]PartitionState {
	dst := make(map[int]PartitionState, len(src))
	for partition, ps := range src {
		dst[partition] = ps
	}
	return dst
}
