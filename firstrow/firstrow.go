// Package firstrow 在变更流上按键保留排序键最小的首条行，
// 并输出首条变化日志，使下游按顺序应用日志即可得到每个键的正确首条。
package firstrow

import (
	"errors"
	"fmt"
	"sync"
)

// Op 表示变更或日志条目的操作类型。
type Op int

const (
	// Insert 写入一行。
	Insert Op = iota
	// Retract 撤回一行。
	Retract
)

func (o Op) String() string {
	switch o {
	case Insert:
		return "INSERT"
	case Retract:
		return "RETRACT"
	}
	return fmt.Sprintf("Op(%d)", int(o))
}

// Change 是变更流中的一条输入变更。
// 排序键为 (Time, ID)：先按 Time 升序（可为负），Time 相同再按 ID 字典序升序。
type Change struct {
	Op   Op
	Key  string
	ID   string
	Time int64
}

// Entry 是输出日志中的一条首条变化记录。
type Entry struct {
	Op   Op
	Key  string
	ID   string
	Time int64
}

func (e Entry) String() string {
	return fmt.Sprintf("%s key=%q id=%q time=%d", e.Op, e.Key, e.ID, e.Time)
}

// 各类可区分的拒绝原因，可用 errors.Is 判定。
var (
	ErrEmptyKey    = errors.New("firstrow: empty key")
	ErrEmptyID     = errors.New("firstrow: empty id")
	ErrDuplicateID = errors.New("firstrow: insert of already live id")
	ErrMissingID   = errors.New("firstrow: retract of unknown id")
	ErrTooManyRows = errors.New("firstrow: live row count exceeds limit")
)

// Deduplicator 维护每个键的存活行集合，并输出首条变化日志。
// 所有方法均可并发调用；同一输入序列反复计算得到完全相同的输出。
type Deduplicator struct {
	mu      sync.RWMutex
	maxLive int
	live    map[string]map[string]int64 // key -> id -> time
	log     []Entry
}

// New 创建一个 Deduplicator，maxLive 为每个键允许的最大存活行数。
func New(maxLive int) *Deduplicator {
	return &Deduplicator{
		maxLive: maxLive,
		live:    make(map[string]map[string]int64),
	}
}

// Apply 处理一批变更：任一条非法则整批拒绝（不改变存活行与日志），
// 否则按顺序应用并返回本批产生的首条变化日志。
func (d *Deduplicator) Apply(batch []Change) ([]Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 在克隆状态上试算整批：任一变更非法即整体拒绝，
	// 存活行与已产生的日志均不受影响。
	live := make(map[string]map[string]int64, len(d.live))
	for key, rows := range d.live {
		cloned := make(map[string]int64, len(rows))
		for id, t := range rows {
			cloned[id] = t
		}
		live[key] = cloned
	}

	var out []Entry
	for i, c := range batch {
		if c.Key == "" {
			return nil, fmt.Errorf("batch[%d]: %w", i, ErrEmptyKey)
		}
		if c.ID == "" {
			return nil, fmt.Errorf("batch[%d]: %w", i, ErrEmptyID)
		}
		rows := live[c.Key]
		if rows == nil {
			rows = make(map[string]int64)
			live[c.Key] = rows
		}
		oldID, oldTime, hadFirst := firstOf(rows)
		switch c.Op {
		case Insert:
			if _, ok := rows[c.ID]; ok {
				return nil, fmt.Errorf("batch[%d]: key=%q id=%q: %w", i, c.Key, c.ID, ErrDuplicateID)
			}
			if d.maxLive > 0 && len(rows)+1 > d.maxLive {
				return nil, fmt.Errorf("batch[%d]: key=%q: %w (max=%d)", i, c.Key, ErrTooManyRows, d.maxLive)
			}
			rows[c.ID] = c.Time
		case Retract:
			if _, ok := rows[c.ID]; !ok {
				return nil, fmt.Errorf("batch[%d]: key=%q id=%q: %w", i, c.Key, c.ID, ErrMissingID)
			}
			delete(rows, c.ID)
		default:
			return nil, fmt.Errorf("batch[%d]: unknown op %d", i, int(c.Op))
		}
		newID, newTime, hasFirst := firstOf(rows)
		// 首条变化时先撤回旧首条、再写入新首条；不变则不输出。
		if hadFirst && (!hasFirst || newID != oldID) {
			out = append(out, Entry{Op: Retract, Key: c.Key, ID: oldID, Time: oldTime})
		}
		if hasFirst && (!hadFirst || newID != oldID) {
			out = append(out, Entry{Op: Insert, Key: c.Key, ID: newID, Time: newTime})
		}
	}

	d.live = live
	d.log = append(d.log, out...)
	return out, nil
}

// Log 返回迄今为止已产生的全部输出日志（拷贝）。
func (d *Deduplicator) Log() []Entry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]Entry, len(d.log))
	copy(out, d.log)
	return out
}

// First 返回指定键当前的首条行；ok 为 false 表示该键无存活行。
func (d *Deduplicator) First(key string) (id string, t int64, ok bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return firstOf(d.live[key])
}

// LiveCount 返回指定键当前的存活行数。
func (d *Deduplicator) LiveCount(key string) int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.live[key])
}

// firstOf 返回 rows 中排序键 (time, id) 最小的行。
func firstOf(rows map[string]int64) (id string, t int64, ok bool) {
	for rid, rt := range rows {
		if !ok || rt < t || (rt == t && rid < id) {
			id, t, ok = rid, rt, true
		}
	}
	return id, t, ok
}
