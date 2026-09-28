// Package timelog 提供按时间戳检索位点的只追加日志。
//
// 日志允许时间戳非单调（乱序）写入；同时维护一个时间索引，
// 仅记录时间戳严格大于此前最大值的消息位点。查询通过索引
// 二分定位，不做逐条扫描，结果与朴素扫描一致且可复现。
package timelog

import (
	"errors"
	"sort"
	"sync"
)

// Position 表示日志中一条消息的位点（从 0 开始的序号）。
type Position uint64

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrNegativeTimestamp 表示追加了负时间戳或按负时间查询。
	ErrNegativeTimestamp = errors.New("timelog: negative timestamp")
	// ErrEmptyPayload 表示追加的消息载荷为空。
	ErrEmptyPayload = errors.New("timelog: empty payload")
	// ErrCapacityExceeded 表示追加会超出日志容量上限。
	ErrCapacityExceeded = errors.New("timelog: capacity exceeded")
)

// Message 是日志中的一条记录。
type Message struct {
	Timestamp int64
	Payload   []byte
}

// indexEntry 是时间索引中的一项：某位点上出现了
// 严格大于此前所有时间戳的新最大值。
type indexEntry struct {
	pos Position
	ts  int64
}

// Log 是支持并发追加与查询的只追加日志。
type Log struct {
	mu       sync.RWMutex
	msgs     []Message
	index    []indexEntry // ts 严格递增
	capacity int
}

// New 创建容量上限为 capacity 的日志；capacity 为 0 表示不限容量。
func New(capacity int) *Log {
	if capacity < 0 {
		capacity = 0
	}
	return &Log{capacity: capacity}
}

// Append 追加一条消息并返回其位点。
// 任何校验失败都整体拒绝，不改变日志、索引与结束位点。
func (l *Log) Append(ts int64, payload []byte) (Position, error) {
	if ts < 0 {
		return 0, ErrNegativeTimestamp
	}
	if len(payload) == 0 {
		return 0, ErrEmptyPayload
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.capacity > 0 && len(l.msgs) >= l.capacity {
		return 0, ErrCapacityExceeded
	}
	pos := Position(len(l.msgs))
	l.msgs = append(l.msgs, Message{
		Timestamp: ts,
		Payload:   append([]byte(nil), payload...),
	})
	// 仅当时间戳严格大于此前最大值时记录索引，相等不记。
	if len(l.index) == 0 || ts > l.index[len(l.index)-1].ts {
		l.index = append(l.index, indexEntry{pos: pos, ts: ts})
	}
	return pos, nil
}

// Query 返回位点最小的、时间戳不小于 t 的消息位点。
// 若无满足条件的消息，返回日志结束位点且 found 为 false。
//
// 首个满足 ts >= t 的消息必为索引项：若位点 p 未入索引，
// 则存在 q < p 使 ts[q] >= ts[p] >= t，与 p 最小矛盾。
// 因此可在严格递增的索引上二分定位，无需逐条扫描。
func (l *Log) Query(t int64) (pos Position, found bool, err error) {
	if t < 0 {
		return 0, false, ErrNegativeTimestamp
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	i := sort.Search(len(l.index), func(i int) bool {
		return l.index[i].ts >= t
	})
	if i == len(l.index) {
		return Position(len(l.msgs)), false, nil
	}
	return l.index[i].pos, true, nil
}

// End 返回日志结束位点（即消息总数）。
func (l *Log) End() Position {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return Position(len(l.msgs))
}

// IndexLen 返回时间索引的项数。
func (l *Log) IndexLen() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.index)
}

// Message 返回位点 pos 处的消息副本。
func (l *Log) Message(pos Position) (Message, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if pos >= Position(len(l.msgs)) {
		return Message{}, false
	}
	m := l.msgs[pos]
	m.Payload = append([]byte(nil), m.Payload...)
	return m, true
}
