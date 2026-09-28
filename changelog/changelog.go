package changelog

import "sync"

// Folder 把按批到达的写入流折叠为撤回式变更日志。
//
// 一个 Folder 内部持有当前表（键 -> 值）与累计日志。
// 所有方法均可被并发调用：Apply 以整批为粒度串行化并原子提交，
// 只读方法与 Apply 互斥，保证读者看到的表与日志彼此一致。
type Folder struct {
	mu      sync.RWMutex
	table   map[string]string
	log     []Entry
	maxLive int
}

// New 创建一个空 Folder。maxLive 为批结束后允许的最大存活键数；
// 传入 0 或负数表示不限制。
func New(maxLive int) *Folder {
	return &Folder{
		table:   make(map[string]string),
		maxLive: maxLive,
	}
}

// Apply 原子地应用一整批变更，返回本批追加的日志条目。
//
// 折叠规则：只比较每个被触及键在批开始前与批结束后的净状态。
// 状态未变则不输出；否则按“先撤回旧值（EntryRetract）、
// 再写入新值（EntryUpsert）”的顺序输出。同一批内多个键按其在批中
// 首次出现的顺序排列。
//
// 批中任何一条变更非法（空键、非法操作）或批结束后存活键数超限，
// 整批都会被拒绝：当前表与已有日志保持不变，返回可区分的哨兵错误，
// Folder 之后仍可继续使用。
func (f *Folder) Apply(batch []Mutation) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// 暂存每个被触及键的批后净状态，全部计算在本地结构上完成，
	// 只有整批合法时才写回 f.table / f.log，从而保证拒绝无副作用。
	order := make([]string, 0, len(batch))
	seen := make(map[string]struct{}, len(batch))
	afterVal := make(map[string]string, len(batch))
	afterLive := make(map[string]bool, len(batch))

	for _, m := range batch {
		if m.Key == "" {
			return nil, ErrEmptyKey
		}
		switch m.Op {
		case OpPut, OpDelete:
		default:
			return nil, ErrInvalidOp
		}

		if _, ok := seen[m.Key]; !ok {
			seen[m.Key] = struct{}{}
			order = append(order, m.Key)
			// 暂存状态初始化为批开始前的当前状态。
			if v, ok := f.table[m.Key]; ok {
				afterVal[m.Key] = v
				afterLive[m.Key] = true
			}
		}

		switch m.Op {
		case OpPut:
			afterVal[m.Key] = m.Value
			afterLive[m.Key] = true
		case OpDelete:
			delete(afterVal, m.Key)
			afterLive[m.Key] = false
		}
	}

	// 计算批结束后存活键数；超限则整批拒绝。
	if f.maxLive > 0 {
		finalLive := len(f.table)
		for _, k := range order {
			_, beforeLive := f.table[k]
			switch {
			case beforeLive && !afterLive[k]:
				finalLive--
			case !beforeLive && afterLive[k]:
				finalLive++
			}
		}
		if finalLive > f.maxLive {
			return nil, ErrTooManyLiveKeys
		}
	}

	// 按首次出现顺序折叠出净变化条目。
	entries := make([]Entry, 0, len(order))
	for _, k := range order {
		oldVal, beforeLive := f.table[k]
		newVal, afterOK := afterVal[k], afterLive[k]
		switch {
		case beforeLive && !afterOK:
			// 存活 -> 不存在：仅撤回旧值。
			entries = append(entries, Entry{Key: k, Kind: EntryRetract, Value: oldVal})
		case !beforeLive && afterOK:
			// 不存在 -> 存活：仅写入新值。
			entries = append(entries, Entry{Key: k, Kind: EntryUpsert, Value: newVal})
		case beforeLive && afterOK && oldVal != newVal:
			// 值发生变化：先撤回旧值，再写入新值。
			entries = append(entries,
				Entry{Key: k, Kind: EntryRetract, Value: oldVal},
				Entry{Key: k, Kind: EntryUpsert, Value: newVal},
			)
		}
		// 状态相同（含值相等）：不输出。
	}

	// 原子提交：写回当前表并追加日志。
	for _, k := range order {
		if afterLive[k] {
			f.table[k] = afterVal[k]
		} else {
			delete(f.table, k)
		}
	}
	f.log = append(f.log, entries...)

	return entries, nil
}

// Snapshot 在同一把锁下返回当前表与截至目前日志的深拷贝，
// 保证并发读者拿到的表与日志逐字段彼此一致。
func (f *Folder) Snapshot() (map[string]string, []Entry) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	table := make(map[string]string, len(f.table))
	for k, v := range f.table {
		table[k] = v
	}
	log := append([]Entry(nil), f.log...)
	return table, log
}

// Log 返回截至目前日志的拷贝。
func (f *Folder) Log() []Entry {
	_, log := f.Snapshot()
	return log
}

// Table 返回当前表的拷贝。
func (f *Folder) Table() map[string]string {
	table, _ := f.Snapshot()
	return table
}

// LiveCount 返回当前存活键数。
func (f *Folder) LiveCount() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.table)
}
