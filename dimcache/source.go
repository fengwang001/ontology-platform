package dimcache

import (
	"context"
	"sync"
)

// SourceRecord 是源头中某个键的状态。Deleted 为墓碑。
type SourceRecord struct {
	Version int64
	Value   string
	Deleted bool
}

// Source 是版本化维表源头（含墓碑）。
type Source struct {
	mu sync.Mutex

	rows   map[string]SourceRecord
	reads  int64
	logger Logger
}

// NewSource 创建源头。maxTrackedKeys<=0 表示不限跟踪键数量。
func NewSource(logger Logger) *Source {
	return &Source{rows: map[string]SourceRecord{}, logger: logger}
}

// Update 插入或更新一行，版本加一（含从墓碑复活）。
func (s *Source) Update(ctx context.Context, key, value string) error {
	if key == "" {
		s.log("source.update.reject", map[string]any{"key": key, "reason": "empty_key"})
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.rows[key]
	rec.Version++
	rec.Value = value
	rec.Deleted = false
	s.rows[key] = rec
	s.log("source.update.ok", map[string]any{"key": key, "version": rec.Version, "value": value})
	return nil
}

// Delete 删除一行：不存在（含从无墓碑）的键整体拒绝；对墓碑重复删除使版本继续递增。
func (s *Source) Delete(ctx context.Context, key string) error {
	if key == "" {
		s.log("source.delete.reject", map[string]any{"key": key, "reason": "empty_key"})
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.rows[key]
	if !ok {
		s.log("source.delete.reject", map[string]any{"key": key, "reason": "delete_missing"})
		return ErrDeleteMissing
	}
	rec.Version++
	rec.Value = ""
	rec.Deleted = true
	s.rows[key] = rec
	s.log("source.delete.ok", map[string]any{"key": key, "version": rec.Version, "tombstone": true})
	return nil
}

// Read 直接读取源头当前状态（不存在时第二个返回值为 false），并计数。
func (s *Source) Read(ctx context.Context, key string) (SourceRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	rec, ok := s.rows[key]
	s.log("source.read", map[string]any{
		"key": key, "found": ok, "version": rec.Version, "deleted": rec.Deleted,
		"reads": s.reads,
	})
	return rec, ok
}

// ReadCount 返回源头被直接读取的次数。
func (s *Source) ReadCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func (s *Source) log(step string, fields map[string]any) {
	if s.logger != nil {
		s.logger.Log(step, fields)
	}
}
