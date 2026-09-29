package lwwview

import (
	"fmt"
	"sort"
	"sync"
)

// Event 是一条按事件时间排序的写入或删除事件。
// Value == nil 表示删除；Value != nil 表示写入（空字符串是合法的存在值）。
type Event struct {
	Key       string
	EventTime int64
	Value     *string
}

// ChangeKind 描述变更日志条目的类型。
type ChangeKind int

const (
	// KindRetract 撤回键此前物化的值（值或墓碑）。
	KindRetract ChangeKind = iota
	// KindUpsert 建立新值：包括非空/空值写入以及删除形成的墓碑。
	KindUpsert
)

// Change 是一条可复现的撤回/建立变更日志记录。
type Change struct {
	Key       string
	Kind      ChangeKind
	EventTime int64
	// OldValue/OldExists 描述撤回前的物化状态。
	OldValue  string
	OldExists bool
	// NewValue/NewExists 描述建立后的物化状态；
	// 删除时 NewExists == false，空值写入时 NewExists == true 且 NewValue == ""。
	NewValue  string
	NewExists bool
}

// Entry 是某个键当前物化的记录。
type Entry struct {
	EventTime int64
	Value     string
	// Exists 为 false 表示键不存在（删除墓碑）；
	// 为 true 且 Value == "" 表示键存在但值为空。
	Exists bool
}

// View 是按事件时间仲裁的最后写入胜出物化视图。
//
// 仲裁规则（按到达顺序逐条执行，结果与到达顺序无关、可复现）：
//   - 事件时间严格大于该键当前物化记录的事件时间才生效；
//   - 事件时间相等或更小一律视为迟到忽略并计入 dropped；
//   - 同事件时间先到者胜（先应用的事件成为当前记录）；
//   - 生效时若键已有记录，先输出一条 Retract，再输出一条 Upsert；
//   - 空值写入（*string 指向 ""）使键存在且值为空；删除使键不存在，
//     二者在内部状态与查询结果中都是可区分的。
type View struct {
	mu      sync.RWMutex
	maxKeys int
	entries map[string]Entry
	changes []Change
	dropped int64
}

// New 创建一个至多容纳 maxKeys 个不同键的视图。
func New(maxKeys int) *View {
	if maxKeys < 0 {
		panic("lwwview: maxKeys must not be negative")
	}
	return &View{
		maxKeys: maxKeys,
		entries: make(map[string]Entry),
	}
}

// Apply 原子地校验并应用整批事件；任一条非法则整批不生效。
// 返回值分别为本批生效的事件数、本批迟到忽略的事件数与拒绝原因。
func (v *View) Apply(events []Event) (applied, dropped int, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if len(events) == 0 {
		return 0, 0, &BatchError{Reason: "invalid argument", Inner: ErrInvalidArgument}
	}

	// 先在影子状态上完成全部校验与模拟：任何一条失败都不得触碰真实状态。
	shadow := make(map[string]Entry, len(v.entries))
	for key, entry := range v.entries {
		shadow[key] = entry
	}

	var simApplied, simDropped int
	simChanges := make([]Change, 0)

	for i, event := range events {
		if event.Key == "" {
			return 0, 0, &BatchError{
				Reason: "empty key",
				Inner:  &EventError{Index: i, Err: ErrEmptyKey},
			}
		}

		current, tracked := shadow[event.Key]
		if !tracked {
			// 新键（含删除墓碑）占用一个容量槽位。
			if len(shadow) >= v.maxKeys {
				return 0, 0, &BatchError{
					Reason: "too many keys",
					Inner:  &EventError{Index: i, Err: ErrTooManyKeys},
				}
			}
		} else if event.EventTime <= current.EventTime {
			// 迟到事件（含同事件时间的后续事件）：先到者胜。
			simDropped++
			continue
		}

		next := Entry{EventTime: event.EventTime}
		if event.Value != nil {
			next.Exists = true
			next.Value = *event.Value
		}

		if tracked {
			simChanges = append(simChanges, Change{
				Key:       event.Key,
				Kind:      KindRetract,
				EventTime: event.EventTime,
				OldValue:  current.Value,
				OldExists: current.Exists,
			})
		}

		simChanges = append(simChanges, Change{
			Key:       event.Key,
			Kind:      KindUpsert,
			EventTime: event.EventTime,
			OldValue:  current.Value,
			OldExists: current.Exists && tracked,
			NewValue:  next.Value,
			NewExists: next.Exists,
		})

		shadow[event.Key] = next
		simApplied++
	}

	// 全部校验通过，提交：状态与变更日志在同一把锁内一次性生效。
	v.entries = shadow
	v.changes = append(v.changes, simChanges...)
	v.dropped += int64(simDropped)

	return simApplied, simDropped, nil
}

// Lookup 查询单个键。
//   - 空值写入：ok == true，entry.Exists == true，entry.Value == ""；
//   - 已删除/从未出现：ok == false。
//
// 删除墓碑的事件时间仍在内部保留，用于继续仲裁迟到事件。
func (v *View) Lookup(key string) (Entry, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	entry, ok := v.entries[key]
	if !ok || !entry.Exists {
		return Entry{}, false
	}
	return entry, true
}

// Snapshot 返回全部“存在”键的深拷贝快照（含空值键，不含删除墓碑）。
func (v *View) Snapshot() map[string]Entry {
	snapshot, _ := v.SnapshotWithDropped()
	return snapshot
}

// Dropped 返回因迟到（事件时间不大于当前事件时间）被忽略的事件数。
func (v *View) Dropped() int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.dropped
}

// KeyCount 返回当前跟踪的键数量（含删除墓碑）。
func (v *View) KeyCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.entries)
}

// ExistingKeyCount 返回当前存在值的键数量（空值键计入，删除墓碑不计入）。
func (v *View) ExistingKeyCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	count := 0
	for _, entry := range v.entries {
		if entry.Exists {
			count++
		}
	}
	return count
}

// Changelog 返回变更日志的深拷贝。
func (v *View) Changelog() []Change {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]Change, len(v.changes))
	copy(out, v.changes)
	return out
}

// SelfCheck 重放变更日志并与当前状态逐项核对。
func (v *View) SelfCheck() error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	replayed := make(map[string]Entry, len(v.entries))

	for i, change := range v.changes {
		current, tracked := replayed[change.Key]

		switch change.Kind {
		case KindRetract:
			if !tracked {
				return fmt.Errorf("lwwview: changelog[%d] retract for untracked key %q", i, change.Key)
			}
			if current.EventTime >= change.EventTime {
				return fmt.Errorf("lwwview: changelog[%d] retract event time %d not greater than %d",
					i, change.EventTime, current.EventTime)
			}
			if current.Value != change.OldValue || current.Exists != change.OldExists {
				return fmt.Errorf("lwwview: changelog[%d] retract state mismatch", i)
			}
		case KindUpsert:
			if tracked && current.EventTime >= change.EventTime {
				return fmt.Errorf("lwwview: changelog[%d] upsert event time not strictly greater", i)
			}
			replayed[change.Key] = Entry{
				EventTime: change.EventTime,
				Value:     change.NewValue,
				Exists:    change.NewExists,
			}
		default:
			return fmt.Errorf("lwwview: changelog[%d] unknown kind %d", i, change.Kind)
		}
	}

	if len(replayed) != len(v.entries) {
		return fmt.Errorf("lwwview: self-check key count mismatch: replayed %d, current %d",
			len(replayed), len(v.entries))
	}
	for key, replayedEntry := range replayed {
		currentEntry, ok := v.entries[key]
		if !ok {
			return fmt.Errorf("lwwview: self-check missing current key %q", key)
		}
		if replayedEntry != currentEntry {
			return fmt.Errorf("lwwview: self-check entry mismatch for key %q: replayed=%+v current=%+v",
				key, replayedEntry, currentEntry)
		}
	}

	return nil
}

// SnapshotWithDropped 返回一致的状态快照与丢弃计数，供并发核对。
// 调用一次即可拿到同一把读锁下逐字段一致的视图状态与 dropped 计数。
func (v *View) SnapshotWithDropped() (map[string]Entry, int64) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	snapshot := make(map[string]Entry)
	for key, entry := range v.entries {
		if !entry.Exists {
			continue
		}
		snapshot[key] = entry
	}
	return snapshot, v.dropped
}

// KeyValue 是有序导出的单个键的物化记录。
type KeyValue struct {
	Key string
	Entry
}

// Entries 按键的字典序返回全部存在键的物化记录，保证输出可复现。
func (v *View) Entries() []KeyValue {
	v.mu.RLock()
	defer v.mu.RUnlock()

	keys := make([]string, 0, len(v.entries))
	for key := range v.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]KeyValue, 0, len(keys))
	for _, key := range keys {
		entry := v.entries[key]
		if !entry.Exists {
			continue
		}
		out = append(out, KeyValue{Key: key, Entry: entry})
	}
	return out
}
