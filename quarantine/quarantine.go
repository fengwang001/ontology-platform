// Package quarantine 实现隔离区：按键保序队列、容量上限与丢弃账。
package quarantine

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrKeyFull 表示该键队列已达每键上限 K。
	ErrKeyFull = errors.New("quarantine: key queue full")
	// ErrFull 表示隔离区总容量 C 已满。
	ErrFull = errors.New("quarantine: zone full")
	// ErrBanned 表示该键累计 Discard 次数已达 Dmax 被封禁。
	ErrBanned = errors.New("quarantine: key banned")
	// ErrNoQueue 表示该键当前没有隔离队列。
	ErrNoQueue = errors.New("quarantine: no queue for key")
)

// Status 为隔离记录状态：队首为 Quarantined，其后为从未判定过的 Held。
type Status int

const (
	Quarantined Status = iota
	Held
)

// Record 为隔离区中的一条记录。
type Record struct {
	Seq        uint64
	Key        string
	Fields     map[string]int64
	Status     Status
	RV         uint64   // 最近一次判定时的规则版本
	Violations []string // 最近一次判定的全部违规规则 id
}

// DropEntry 为丢弃日志条目。
type DropEntry struct {
	Seq        uint64
	Key        string
	RV         uint64
	Violations []string
}

// Zone 为隔离区。非并发安全，由上层 flow 串行化。
type Zone struct {
	capC     int
	capK     int
	dmax     int
	queues   map[string][]*Record
	total    int
	discards map[string]int
	dropped  []DropEntry
}

// New 构造隔离区；参数须满足 1<=K<=C<=1e5、1<=Dmax<=1000。
func New(c, k, dmax int) (*Zone, error) {
	if c < 1 || c > 100000 || k < 1 || k > c || dmax < 1 || dmax > 1000 {
		return nil, fmt.Errorf("quarantine: invalid capacity C=%d K=%d Dmax=%d", c, k, dmax)
	}
	return &Zone{
		capC:     c,
		capK:     k,
		dmax:     dmax,
		queues:   make(map[string][]*Record),
		discards: make(map[string]int),
	}, nil
}

// C 返回隔离区总容量。
func (z *Zone) C() int { return z.capC }

// K 返回每键队列上限。
func (z *Zone) K() int { return z.capK }

// Len 返回该键队列长度。
func (z *Zone) Len(key string) int { return len(z.queues[key]) }

// Total 返回隔离区内记录总数（Quarantined 与 Held 都算）。
func (z *Zone) Total() int { return z.total }

// HasQueue 报告该键当前是否有非空队列。
func (z *Zone) HasQueue(key string) bool { return len(z.queues[key]) > 0 }

// Front 返回队首；调用方保证队列非空。
func (z *Zone) Front(key string) *Record { return z.queues[key][0] }

// Banned 报告该键是否已被封禁。
func (z *Zone) Banned(key string) bool { return z.discards[key] >= z.dmax }

// DiscardCount 返回该键累计 Discard 次数。
func (z *Zone) DiscardCount(key string) int { return z.discards[key] }

// CheckAdd 校验入队容量，不修改状态；ErrKeyFull 先于 ErrFull。
func (z *Zone) CheckAdd(key string) error {
	if len(z.queues[key]) >= z.capK {
		return fmt.Errorf("%w: key %q", ErrKeyFull, key)
	}
	if z.total >= z.capC {
		return fmt.Errorf("%w: key %q", ErrFull, key)
	}
	return nil
}

// Push 把记录排到该键队尾；调用方须先 CheckAdd。
func (z *Zone) Push(rec *Record) {
	z.queues[rec.Key] = append(z.queues[rec.Key], rec)
	z.total++
}

// Pop 移除并返回队首；调用方保证队列非空。排空后删除该键的队列。
func (z *Zone) Pop(key string) *Record {
	q := z.queues[key]
	rec := q[0]
	q[0] = nil
	if len(q) == 1 {
		delete(z.queues, key)
	} else {
		z.queues[key] = q[1:]
	}
	z.total--
	return rec
}

// Keys 返回当前有非空队列的键，按键字节序排列。
func (z *Zone) Keys() []string {
	keys := make([]string, 0, len(z.queues))
	for k := range z.queues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Discard 把队首移入丢弃日志并累计丢弃账；达到 Dmax 的键即被封禁。
func (z *Zone) Discard(key string) DropEntry {
	rec := z.Pop(key)
	e := DropEntry{
		Seq:        rec.Seq,
		Key:        rec.Key,
		RV:         rec.RV,
		Violations: append([]string(nil), rec.Violations...),
	}
	z.dropped = append(z.dropped, e)
	z.discards[key]++
	return e
}

// Dropped 返回丢弃日志的副本。
func (z *Zone) Dropped() []DropEntry {
	return append([]DropEntry(nil), z.dropped...)
}

// Snapshot 按键字节序、队内保序返回隔离区全部记录的副本。
func (z *Zone) Snapshot() []Record {
	var out []Record
	for _, k := range z.Keys() {
		for _, rec := range z.queues[k] {
			out = append(out, *rec)
		}
	}
	return out
}
