package changelog

import (
	"errors"
	"strconv"
	"sync"
)

// ErrInvalidLimit 在 maxLiveKeys <= 0 时由 NewFolder 返回。
var ErrInvalidLimit = errors.New("changelog: maxLiveKeys must be positive")

// Folder 把按批到达的写入流折叠为撤回式变更日志。
//
// 一个 Folder 同时维护「当前表」（键 -> 值，键要么不存在要么恰有一个值）
// 与「已产生的日志」（有序条目序列）。Apply 可被并发调用，每批整体原子：
// 被拒绝的批不会改变当前表与日志。
type Folder struct {
	mu          sync.RWMutex
	maxLiveKeys int
	table       map[string]string
	log         []Entry
}

// NewFolder 创建一个存活键数上限为 maxLiveKeys 的 Folder。
func NewFolder(maxLiveKeys int) (*Folder, error) {
	if maxLiveKeys <= 0 {
		return nil, ErrInvalidLimit
	}
	return &Folder{
		maxLiveKeys: maxLiveKeys,
		table:       make(map[string]string),
	}, nil
}

// Apply 原子地应用一批写入，返回该批折叠出的日志条目（先撤回后写入，
// 多键按首次出现顺序）。批非法时返回 *RejectError，且不改变任何状态。
func (f *Folder) Apply(batch []Write) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// order 记录被触及键的首次出现顺序；final 记录每个被触及键在批结束后的
	// 状态：键存在于 map 中即表示存活，值为其最终值。
	order := make([]string, 0, len(batch))
	touched := make(map[string]bool, len(batch))
	final := make(map[string]string, len(batch))

	for i, w := range batch {
		if w.Key == "" {
			return nil, &RejectError{
				Reason: RejectEmptyKey,
				Index:  i,
				Detail: "write at index " + strconv.Itoa(i) + " has empty key",
			}
		}
		if w.Op != OpPut && w.Op != OpDelete {
			return nil, &RejectError{
				Reason: RejectInvalidOp,
				Index:  i,
				Detail: "write at index " + strconv.Itoa(i) + " has invalid op " + strconv.Itoa(int(w.Op)),
			}
		}
		if !touched[w.Key] {
			touched[w.Key] = true
			order = append(order, w.Key)
		}
		if w.Op == OpPut {
			final[w.Key] = w.Value
		} else {
			delete(final, w.Key)
		}
	}

	// 仅依据批开始前 / 批结束后的净状态计算存活键数与折叠条目。
	postCount := len(f.table)
	entries := make([]Entry, 0, 2*len(order))
	for _, k := range order {
		oldVal, existedBefore := f.table[k]
		newVal, existsAfter := final[k]
		if !existedBefore && existsAfter {
			postCount++
		} else if existedBefore && !existsAfter {
			postCount--
		}
		if existedBefore == existsAfter && oldVal == newVal {
			// 状态相同（都不存在，或都存在且值相等）：不输出。
			continue
		}
		if existedBefore {
			entries = append(entries, Entry{Kind: EntryRetract, Key: k, Value: oldVal})
		}
		if existsAfter {
			entries = append(entries, Entry{Kind: EntryInsert, Key: k, Value: newVal})
		}
	}

	if postCount > f.maxLiveKeys {
		return nil, &RejectError{
			Reason: RejectTooManyLiveKeys,
			Index:  -1,
			Detail: "live keys after batch = " + strconv.Itoa(postCount) +
				", limit = " + strconv.Itoa(f.maxLiveKeys),
		}
	}

	// 校验通过后才提交：先更新表，再追加日志，二者在同一把锁内完成。
	for _, k := range order {
		if v, ok := final[k]; ok {
			f.table[k] = v
		} else {
			delete(f.table, k)
		}
	}
	f.log = append(f.log, entries...)

	out := make([]Entry, len(entries))
	copy(out, entries)
	return out, nil
}

// Snapshot 返回当前表的深拷贝与当前日志的深拷贝。
// 两份快照取自同一个已完整应用的批之后，彼此一致。
func (f *Folder) Snapshot() (map[string]string, []Entry) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	table := make(map[string]string, len(f.table))
	for k, v := range f.table {
		table[k] = v
	}
	log := make([]Entry, len(f.log))
	copy(log, f.log)
	return table, log
}

// Log 返回已产生日志的拷贝。
func (f *Folder) Log() []Entry {
	_, log := f.Snapshot()
	return log
}

// Table 返回当前表的拷贝。
func (f *Folder) Table() map[string]string {
	table, _ := f.Snapshot()
	return table
}

// LiveKeyCount 返回当前存活键数量。
func (f *Folder) LiveKeyCount() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.table)
}
