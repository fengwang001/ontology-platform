package segmentlog

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// record 是日志内部保存的不可变记录。data 为追加时内容的副本。
type record struct {
	time  time.Time
	data  []byte
	bytes int64
	start int64 // 该记录首字节在日志全局字节流中的位点
}

// segment 是一段首尾相接、只在活动时可写的记录区间。
type segment struct {
	id        int
	records   []record
	bytes     int64
	startOff  int64
	endOff    int64
	firstTime time.Time
	lastTime  time.Time
	active    bool
}

func (s *segment) info() SegmentInfo {
	return SegmentInfo{
		ID:        s.id,
		StartOff:  s.startOff,
		EndOff:    s.endOff,
		Bytes:     s.bytes,
		FirstTime: s.firstTime,
		LastTime:  s.lastTime,
		RecordNum: len(s.records),
		Active:    s.active,
	}
}

// state 是受互斥锁保护的全部可变状态。
type state struct {
	cfg      Config
	segments []*segment
	total    int64
	startOff int64
	nextID   int
	lastTime time.Time
	debug    io.Writer
}

// Log 是并发安全的分段日志。零值不可用，必须经 New 创建。
type Log struct {
	mu    sync.RWMutex
	state state
}

// New 校验配置并创建空日志。
//
// MaxSegmentBytes 必须为正数；MaxAge 与 MaxTotalBytes 为 0 表示关闭对应
// 保留维度，若给定则必须为正。
func New(cfg Config, now time.Time) (*Log, error) {
	if cfg.MaxSegmentBytes <= 0 {
		return nil, fmt.Errorf("%w: max segment bytes must be positive, got %d", ErrInvalidConfig, cfg.MaxSegmentBytes)
	}
	if cfg.MaxAge < 0 {
		return nil, fmt.Errorf("%w: max age must be zero (disabled) or positive, got %s", ErrInvalidConfig, cfg.MaxAge)
	}
	if cfg.MaxTotalBytes < 0 {
		return nil, fmt.Errorf("%w: max total bytes must be zero (disabled) or positive, got %d", ErrInvalidConfig, cfg.MaxTotalBytes)
	}
	l := &Log{
		state: state{
			cfg:      cfg,
			segments: make([]*segment, 0),
			lastTime: now,
		},
	}
	l.state.debugf("NEW cfg={seg=%d age=%s total=%d} now=%s", cfg.MaxSegmentBytes, cfg.MaxAge, cfg.MaxTotalBytes, formatTime(now))
	return l, nil
}

// SetDebugOutput 打开逐步判定日志；传 nil 关闭。应在并发使用前配置。
func (l *Log) SetDebugOutput(w io.Writer) {
	l.mu.Lock()
	l.state.debug = w
	l.mu.Unlock()
}

// Append 原子追加一条记录。
//
// 活动段不存在或加入该记录后超出 MaxSegmentBytes 时，先新建活动段再写入；
// 因此单条不超过段容量的记录总能被接受。任何校验失败都在状态变更之前
// 返回，段列表、总字节数与起始位点保持不变。
func (l *Log) Append(rec Record) (offset int64, err error) {
	if err := validateRecord(rec, l.state.cfg.MaxSegmentBytes); err != nil {
		return 0, err
	}

	data := append([]byte(nil), rec.Data...)

	l.mu.Lock()
	defer l.mu.Unlock()

	s := &l.state
	if !rec.Time.Equal(s.lastTime) && rec.Time.Before(s.lastTime) {
		s.debugf("APPEND REJECT clock-backwards rec.time=%s last=%s", formatTime(rec.Time), formatTime(s.lastTime))
		return 0, fmt.Errorf("%w: record time %s is before last accepted time %s", ErrClockBackwards, formatTime(rec.Time), formatTime(s.lastTime))
	}

	var active *segment
	if len(s.segments) > 0 {
		active = s.segments[len(s.segments)-1]
	}
	if active == nil || active.bytes+rec.Bytes > s.cfg.MaxSegmentBytes {
		active = &segment{
			id:       s.nextID,
			startOff: s.startOff + s.total,
			endOff:   s.startOff + s.total,
			active:   true,
		}
		s.nextID++
		if len(s.segments) > 0 {
			s.segments[len(s.segments)-1].active = false
		}
		s.segments = append(s.segments, active)
		s.debugf("APPEND rollover new-segment=%s", describe(active.info()))
	}

	offset = active.endOff
	active.records = append(active.records, record{
		time:  rec.Time,
		data:  data,
		bytes: rec.Bytes,
		start: offset,
	})
	active.bytes += rec.Bytes
	active.endOff += rec.Bytes
	if len(active.records) == 1 {
		active.firstTime = rec.Time
	}
	active.lastTime = rec.Time
	s.total += rec.Bytes
	if rec.Time.After(s.lastTime) {
		s.lastTime = rec.Time
	}

	s.debugf("APPEND OK offset=%d bytes=%d time=%s -> %s", offset, rec.Bytes, formatTime(rec.Time), s.describeSegments())
	return offset, nil
}

func validateRecord(rec Record, maxSegmentBytes int64) error {
	if rec.Time.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidRecord)
	}
	if len(rec.Data) == 0 {
		return fmt.Errorf("%w: empty payload", ErrInvalidRecord)
	}
	if rec.Bytes <= 0 {
		return fmt.Errorf("%w: byte size must be positive, got %d", ErrInvalidRecord, rec.Bytes)
	}
	if rec.Bytes != int64(len(rec.Data)) {
		return fmt.Errorf("%w: declared bytes %d != payload length %d", ErrInvalidRecord, rec.Bytes, len(rec.Data))
	}
	if rec.Bytes > maxSegmentBytes {
		return fmt.Errorf("%w: record bytes %d exceed segment capacity %d", ErrInvalidRecord, rec.Bytes, maxSegmentBytes)
	}
	return nil
}

// StartOffset 返回当前日志起始位点，只进不退。
func (l *Log) StartOffset() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.state.startOff
}

// TotalBytes 返回现存记录计入保留策略的字节总数。
func (l *Log) TotalBytes() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.state.total
}

// Segments 返回现存段的只读快照（旧 → 新）。
func (l *Log) Segments() []SegmentInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	infos := make([]SegmentInfo, 0, len(l.state.segments))
	for _, seg := range l.state.segments {
		infos = append(infos, seg.info())
	}
	return infos
}

// LastTime 返回已接受的最大记录时间戳（主要用于观测/测试）。
func (l *Log) LastTime() time.Time {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.state.lastTime
}

func (s *state) describeSegments() string {
	parts := make([]string, 0, len(s.segments))
	for _, seg := range s.segments {
		parts = append(parts, describe(seg.info()))
	}
	return "segments=[" + strings.Join(parts, " ") + "]"
}

func describe(info SegmentInfo) string {
	flag := ""
	if info.Active {
		flag = " active"
	}
	return fmt.Sprintf("{id=%d off=[%d,%d) bytes=%d n=%d last=%s%s}",
		info.ID, info.StartOff, info.EndOff, info.Bytes, info.RecordNum, formatTime(info.LastTime), flag)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("15:04:05.000")
}

func (s *state) debugf(format string, args ...any) {
	if s.debug != nil {
		fmt.Fprintf(s.debug, "[segmentlog] "+format+"\n", args...)
	}
}
