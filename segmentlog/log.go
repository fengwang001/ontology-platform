package segmentlog

import (
	"fmt"
	"log"
	"time"
)

func validateConfig(cfg Config) error {
	if cfg.MaxSegmentBytes <= 0 {
		return fmt.Errorf("%w: MaxSegmentBytes must be > 0, got %d", ErrInvalidConfig, cfg.MaxSegmentBytes)
	}
	if cfg.MaxAge < 0 {
		return fmt.Errorf("%w: MaxAge must be >= 0, got %s", ErrInvalidConfig, cfg.MaxAge)
	}
	if cfg.MaxTotalBytes < 0 {
		return fmt.Errorf("%w: MaxTotalBytes must be >= 0, got %d", ErrInvalidConfig, cfg.MaxTotalBytes)
	}
	return nil
}

func validateRecord(r Record) error {
	if r.Timestamp.IsZero() {
		return fmt.Errorf("%w: zero timestamp", ErrInvalidRecord)
	}
	if r.Size <= 0 {
		return fmt.Errorf("%w: size must be > 0, got %d", ErrInvalidRecord, r.Size)
	}
	if r.Data == nil {
		return fmt.Errorf("%w: nil data", ErrInvalidRecord)
	}
	if len(r.Data) != r.Size {
		return fmt.Errorf("%w: data length %d does not match declared size %d", ErrInvalidRecord, len(r.Data), r.Size)
	}
	return nil
}

// discardLogger 用于调用方未提供 logger 时。
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// New 创建分段日志。非法配置返回包装了 ErrInvalidConfig 的错误，且不产生任何副作用。
func New(cfg Config, logger Logger) (*Log, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = log.New(discardWriter{}, "", log.LstdFlags|log.Lmicroseconds)
	}
	l := &Log{
		cfg:       cfg,
		logger:    logger,
		nextSegID: 1,
	}
	logger.Printf("init: config maxSegmentBytes=%d maxAge=%s maxTotalBytes=%d; no segments yet",
		cfg.MaxSegmentBytes, cfg.MaxAge, cfg.MaxTotalBytes)
	return l, nil
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// Append 追加一条记录，返回该记录在日志中的起始字节位点。
// 任何校验失败都整体拒绝，段列表、总字节数与起始位点保持不变。
func (l *Log) Append(r Record) (int64, error) {
	if err := validateRecord(r); err != nil {
		l.logger.Printf("append REJECT: %v; state unchanged", err)
		return 0, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.maxTime.IsZero() && r.Timestamp.Before(l.maxTime) {
		err := fmt.Errorf("%w: record ts=%s earlier than accepted max ts=%s",
			ErrClockBackward, r.Timestamp.Format(time.RFC3339Nano), l.maxTime.Format(time.RFC3339Nano))
		l.logger.Printf("append REJECT: %v; state unchanged", err)
		return 0, err
	}

	// 记录复制，避免调用方在调用后修改底层切片。
	stored := Record{Timestamp: r.Timestamp, Size: r.Size, Data: make([]byte, len(r.Data))}
	copy(stored.Data, r.Data)

	if l.active == nil || l.active.bytes+stored.Size > l.cfg.MaxSegmentBytes {
		seg := &segment{id: l.nextSegID, startOff: l.nextOff, endOff: l.nextOff}
		l.nextSegID++
		l.segs = append(l.segs, seg)
		l.active = seg
		l.logger.Printf("append: roll to new segment #%d (active bytes=%d, record size=%d, segment cap=%d)",
			seg.id, seg.bytes, stored.Size, l.cfg.MaxSegmentBytes)
	}

	recOff := l.nextOff
	l.active.records = append(l.active.records, stored)
	l.active.bytes += stored.Size
	l.active.endOff += int64(stored.Size)
	if stored.Timestamp.After(l.active.maxTime) {
		l.active.maxTime = stored.Timestamp
	}
	l.totalBytes += int64(stored.Size)
	l.nextOff += int64(stored.Size)
	if stored.Timestamp.After(l.maxTime) {
		l.maxTime = stored.Timestamp
	}

	l.logger.Printf("append OK: seg=#%d off=%d size=%d ts=%s; segments=%s totalBytes=%d range=[%d,%d)",
		l.active.id, recOff, stored.Size, stored.Timestamp.Format(time.RFC3339Nano),
		l.snapshotSegmentsLocked(), l.totalBytes, l.startOff, l.nextOff)
	return recOff, nil
}
