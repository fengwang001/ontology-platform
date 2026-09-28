// Package timelog 在时间戳不保证单调的只追加日志上提供按时间戳查位点的能力。
package timelog

import (
	"sort"
	"sync"
)

// Message 是日志中的一条消息。Timestamp 可与已有时间戳相等或回退。
type Message struct {
	Timestamp int64
	Payload   []byte
}

// Options 配置日志容量。MaxEntries <= 0 表示不限制。
type Options struct {
	MaxEntries int
}

// SeekResult 是一次按时间戳查询的结果。
type SeekResult struct {
	// Offset 命中时为时间戳不小于目标时间的最小消息位点；
	// 未命中时为日志结束位点（已有消息条数）。
	Offset int64
	// Found 表示是否存在满足条件的消息。
	Found bool
}

// indexEntry 记录“时间戳严格大于此前最大值”时的位点。
type indexEntry struct {
	timestamp int64
	offset    int64
}

// Log 是并发安全的只追加日志，附带严格新高时间索引。
type Log struct {
	_ noCopy

	mu       sync.RWMutex
	messages []Message
	index    []indexEntry
	max      int64
	limit    int
	hasMax   bool
}

// noCopy 嵌入后由 go vet 检查值拷贝。
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// New 创建空日志。MaxEntries < 0 属于非法配置；MaxEntries == 0 表示不限制。
func New(opts Options) (*Log, error) {
	if opts.MaxEntries < 0 {
		return nil, newError(KindInvalidArgument, "New", "MaxEntries must be >= 0")
	}
	return &Log{limit: opts.MaxEntries}, nil
}

// Append 原子追加一批消息。空批次为非法参数；批次内任一消息时间戳为负则整批拒绝；
// 追加后条数超过容量则整批拒绝。任何拒绝都不改变已有消息、结束位点与索引。
func (l *Log) Append(batch []Message) error {
	if len(batch) == 0 {
		return newError(KindInvalidArgument, "Append", "empty batch")
	}
	for _, msg := range batch {
		if msg.Timestamp < 0 {
			return newError(KindNegativeTimestamp, "Append",
				"negative timestamp in batch element")
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.limit > 0 && len(l.messages)+len(batch) > l.limit {
		return newError(KindCapacityExceeded, "Append", "log capacity exceeded")
	}

	base := int64(len(l.messages))
	for i, msg := range batch {
		offset := base + int64(i)
		if !l.hasMax || msg.Timestamp > l.max {
			l.index = append(l.index, indexEntry{timestamp: msg.Timestamp, offset: offset})
			l.max = msg.Timestamp
			l.hasMax = true
		}
	}
	l.messages = append(l.messages, batch...)
	return nil
}

// Seek 经时间索引（不逐条扫描）返回位点最小的、时间戳不小于 ts 的消息位点。
// 不存在满足条件的消息时返回日志结束位点并标记 Found=false。负的查询时间被拒绝。
func (l *Log) SeekByTimestamp(ts int64) (SeekResult, error) {
	if ts < 0 {
		return SeekResult{}, newError(KindNegativeTimestamp, "Seek", "negative query timestamp")
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	end := int64(len(l.messages))
	// 索引时间戳严格递增；取首个 timestamp >= ts 的索引项。
	i := sort.Search(len(l.index), func(i int) bool { return l.index[i].timestamp >= ts })
	if i == len(l.index) {
		return SeekResult{Offset: end, Found: false}, nil
	}
	return SeekResult{Offset: l.index[i].offset, Found: true}, nil
}

// EndOffset 返回日志结束位点（已有消息条数），即下一条消息将占据的位点。
func (l *Log) EndOffset() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return int64(len(l.messages))
}
