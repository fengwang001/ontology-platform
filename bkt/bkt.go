// Package bkt 提供按本地日历日分桶与计数，依赖 tzone。
package bkt

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/tzone"
)

var (
	// ErrNegativeTS 表示事件时间为负。
	ErrNegativeTS = errors.New("bkt: negative event time")
	// ErrEmptyKey 表示事件 Key 为空串。
	ErrEmptyKey = errors.New("bkt: empty event key")
)

// Event 是一条待分桶的事件，EventTime 为 Unix 秒。
type Event struct {
	EventTime int64
	Key       string
}

// Bucketer 把事件按本地日历日分桶并预聚合计数。
type Bucketer struct {
	z *tzone.Zone

	mu     sync.RWMutex
	counts map[string]int

	// lastScan 记录最近一次 Count 扫描过的事件个数（非导出，仅供包内测试断言）。
	lastScan atomic.Int64
}

// New 创建一个基于时区 z 的分桶器。
func New(z *tzone.Zone) *Bucketer {
	return &Bucketer{z: z, counts: make(map[string]int)}
}

// Bucket 返回 ts 的本地日历日期；ts 为负时返回 ErrNegativeTS，无副作用。
func (b *Bucketer) Bucket(ts int64) (string, error) {
	if ts < 0 {
		return "", ErrNegativeTS
	}
	return b.z.Date(ts), nil
}

// Feed 把事件归入各自的桶并累计计数。先整体校验，任一事件非法则
// 整体失败、状态不变。
func (b *Bucketer) Feed(events []Event) error {
	for _, e := range events {
		if e.EventTime < 0 {
			return ErrNegativeTS
		}
		if e.Key == "" {
			return ErrEmptyKey
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range events {
		b.counts[b.z.Date(e.EventTime)]++
	}
	return nil
}

// Count 返回某桶的事件数。计数在 Feed 时已按桶预聚合，
// 这里是 O(1) 查表，扫描事件数为 0。
func (b *Bucketer) Count(date string) int {
	b.mu.RLock()
	n := b.counts[date]
	b.mu.RUnlock()
	b.lastScan.Store(0)
	return n
}
