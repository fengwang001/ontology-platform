package firstrow

import (
	"sort"
	"sync"
)

// DefaultMaxRowsPerKey 为每个键允许的存活行数上限默认值。
const DefaultMaxRowsPerKey = 1000

// Deduper 在变更流上按键保留排序键最小的首条存活行。
// 一个 Deduper 可被并发读写；同一输入序列反复计算得到完全相同的输出。
type Deduper struct {
	mu      sync.RWMutex
	maxRows int
	logger  *DecisionLogger
	keyRows map[string]map[string]int64
}

// New 创建 Deduper。maxRowsPerKey <= 0 时使用 DefaultMaxRowsPerKey。
func New(maxRowsPerKey int, logger *DecisionLogger) *Deduper {
	if maxRowsPerKey <= 0 {
		maxRowsPerKey = DefaultMaxRowsPerKey
	}
	if logger == nil {
		logger = NewLogger(nil)
	}
	return &Deduper{
		maxRows: maxRowsPerKey,
		logger:  logger,
		keyRows: make(map[string]map[string]int64),
	}
}

// Apply 原子地处理一批变更，返回该批产生的输出（先撤回旧首条、再写入新首条）。
// 批中任一变更非法时整批拒绝：存活行与日志均不改变，并返回 *RejectedChangeError。
func (d *Deduper) Apply(changes []Change) ([]Output, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(changes) == 0 {
		return nil, nil
	}

	// 工作副本：仅在整批全部合法后才提交到真实状态。
	work := make(map[string]map[string]int64)
	rowsOf := func(key string) map[string]int64 {
		rows, ok := work[key]
		if !ok {
			rows = make(map[string]int64)
			for id, t := range d.keyRows[key] {
				rows[id] = t
			}
			work[key] = rows
		}
		return rows
	}

	type effect struct {
		entry   LogEntry
		emitted []Output
	}
	effects := make([]effect, 0, len(changes))
	emittedAll := make([]Output, 0, len(changes)*2)

	for i, ch := range changes {
		if ch.Key == "" {
			return nil, &RejectedChangeError{Index: i, Reason: ReasonEmptyKey}
		}
		if ch.ID == "" {
			return nil, &RejectedChangeError{Index: i, Reason: ReasonEmptyID}
		}

		rows := rowsOf(ch.Key)
		oldFirst := firstRow(ch.Key, rows)

		switch ch.Op {
		case Insert:
			if _, live := rows[ch.ID]; live {
				return nil, &RejectedChangeError{Index: i, Reason: ReasonDuplicateID}
			}
			if len(rows) >= d.maxRows {
				return nil, &RejectedChangeError{Index: i, Reason: ReasonTooManyRows}
			}
			rows[ch.ID] = ch.EventTime
		case Retract:
			if _, live := rows[ch.ID]; !live {
				return nil, &RejectedChangeError{Index: i, Reason: ReasonMissingID}
			}
			delete(rows, ch.ID)
		default:
			return nil, &RejectedChangeError{Index: i, Reason: ReasonInvalidOp}
		}

		newFirst := firstRow(ch.Key, rows)
		e := effect{entry: LogEntry{
			Change:   ch,
			Accepted: true,
			OldFirst: oldFirst,
			NewFirst: newFirst,
		}}
		if !sameRow(oldFirst, newFirst) {
			if oldFirst != nil {
				e.emitted = append(e.emitted, Output{
					Kind: Delete, Key: oldFirst.Key, ID: oldFirst.ID, EventTime: oldFirst.EventTime,
				})
			}
			if newFirst != nil {
				e.emitted = append(e.emitted, Output{
					Kind: Upsert, Key: newFirst.Key, ID: newFirst.ID, EventTime: newFirst.EventTime,
				})
			}
			e.entry.Emitted = e.emitted
			emittedAll = append(emittedAll, e.emitted...)
		}
		effects = append(effects, e)
	}

	// 提交存活行。
	for key, rows := range work {
		if len(rows) == 0 {
			delete(d.keyRows, key)
			continue
		}
		committed := make(map[string]int64, len(rows))
		for id, t := range rows {
			committed[id] = t
		}
		d.keyRows[key] = committed
	}

	// 提交日志（被拒绝的批不会执行到这里，日志保持不变）。
	entries := make([]LogEntry, len(effects))
	for i, e := range effects {
		entries[i] = e.entry
	}
	d.logger.commit(entries)

	return emittedAll, nil
}

// FirstRow 返回某个键当前首条存活行；不存在时 ok 为 false。
func (d *Deduper) FirstRow(key string) (id string, eventTime int64, ok bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if r := firstRow(key, d.keyRows[key]); r != nil {
		return r.ID, r.EventTime, true
	}
	return "", 0, false
}

// Snapshot 返回所有键当前首条的一致快照，按键升序排列；无存活行的键不出现。
func (d *Deduper) Snapshot() []Row {
	d.mu.RLock()
	defer d.mu.RUnlock()
	keys := make([]string, 0, len(d.keyRows))
	for key, rows := range d.keyRows {
		if len(rows) > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]Row, 0, len(keys))
	for _, key := range keys {
		r := firstRow(key, d.keyRows[key])
		if r != nil {
			out = append(out, *r)
		}
	}
	return out
}

// Log 返回到目前为止判定日志的一份副本。
func (d *Deduper) Log() []LogEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.logger.entries()
}

// firstRow 返回排序键（eventTime 升序，并列时 id 字典序升序）最小的存活行。
func firstRow(key string, rows map[string]int64) *Row {
	var best *Row
	for id, t := range rows {
		if best == nil || t < best.EventTime || (t == best.EventTime && id < best.ID) {
			best = &Row{Key: key, ID: id, EventTime: t}
		}
	}
	return best
}

func sameRow(a, b *Row) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Key == b.Key && a.ID == b.ID && a.EventTime == b.EventTime
}
