// Package closedts 实现副本组的闭合时间戳跟踪：主副本在写入进行中持续发布
// 「不会再有时间戳不大于闭合时间戳的写」的承诺，从副本据此就地服务历史读。
package closedts

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因。被拒绝的操作不会改变任何副本状态。
var (
	// ErrNegativeLag 目标滞后为负。
	ErrNegativeLag = errors.New("closedts: 目标滞后不能为负")
	// ErrNegativeTimestamp 时间戳为负。
	ErrNegativeTimestamp = errors.New("closedts: 时间戳不能为负")
	// ErrReplicaNotFound 指向不存在的副本。
	ErrReplicaNotFound = errors.New("closedts: 副本不存在")
	// ErrNotClosed 没有任何已收到发布的闭合时间戳不小于读时间戳。
	ErrNotClosed = errors.New("closedts: 未闭合")
	// ErrNotCaughtUp 已有闭合时间戳不小于读时间戳的发布，但从副本尚未应用够日志序号。
	ErrNotCaughtUp = errors.New("closedts: 未追上")
)

// Clock 注入的时钟，单位与时间戳一致。
type Clock interface {
	Now() int64
}

// Message 经注入网络送达从副本的消息。
type Message interface{ isMessage() }

// LogEntry 主副本应用后广播给从副本的写日志。
type LogEntry struct {
	Seq   uint64 // 提案时分配的连续日志序号
	TS    int64  // 写的最终时间戳（可能已被抬高）
	Key   string
	Value string
}

func (LogEntry) isMessage() {}

// Publish 主副本发布的闭合时间戳承诺。
type Publish struct {
	Closed int64  // 闭合时间戳：不会再有时间戳不大于它的写
	MaxSeq uint64 // 发布此刻已分配的最大日志序号
}

func (Publish) isMessage() {}

// Network 注入的网络，允许乱序、重复投递。
type Network interface {
	Send(to int, msg Message)
}

// ManualClock 可手动推进的时钟，便于确定性重放。
type ManualClock struct {
	mu  sync.Mutex
	now int64
}

// NewManualClock 返回从 start 开始的手动时钟。
func NewManualClock(start int64) *ManualClock {
	return &ManualClock{now: start}
}

// Now 实现 Clock。
func (c *ManualClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 将时钟前进 delta（可为 0）。
func (c *ManualClock) Advance(delta int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += delta
}

// versionedValue 主/从副本上的一条已应用写。
type versionedValue struct {
	ts    int64
	seq   uint64
	value string
}

// readAt 在已应用的写中读取时间戳 t 上的值：
// 取 ts <= t 中（ts, seq）最大者，与主副本全部写应用后的读一致。
func readAt(data map[string][]versionedValue, key string, t int64) (string, bool) {
	best := versionedValue{ts: -1}
	found := false
	for _, v := range data[key] {
		if v.ts > t {
			continue
		}
		if !found || v.ts > best.ts || (v.ts == best.ts && v.seq > best.seq) {
			best = v
			found = true
		}
	}
	return best.value, found
}
