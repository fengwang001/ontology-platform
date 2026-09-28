// Package changelog 把按批到达的写入流折叠为撤回式变更日志（retract-then-upsert）。
//
// 折叠规则：一批写入只看每个被触及键在批开始前与批结束后的净状态。
// 状态相同则不输出；状态不同则按“先撤回旧值、再写入新值”的顺序输出条目；
// 同一批内多个键按该键在批中首次出现的顺序排列。
package changelog

import (
	"fmt"
	"sync"
)

// Op 表示对单个键的操作类型。
type Op string

const (
	// OpPut 写入（使键存在）。
	OpPut Op = "put"
	// OpDelete 删除（使键不存在）。
	OpDelete Op = "delete"
)

// Write 是批内的一条写入。
type Write struct {
	Key   string
	Op    Op
	Value string // 仅 OpPut 时有效
}

// EntryKind 是变更日志条目的种类。
type EntryKind int

const (
	// KindRetract 撤回旧值。
	KindRetract EntryKind = iota
	// KindUpsert 写入新值。
	KindUpsert
)

func (k EntryKind) String() string {
	switch k {
	case KindRetract:
		return "retract"
	case KindUpsert:
		return "upsert"
	default:
		return "unknown"
	}
}

// Entry 是变更日志中的一条记录。
// Kind=KindRetract 时 Value 为被撤回的旧值；
// Kind=KindUpsert 时 Value 为新写入的值。
type Entry struct {
	Seq   int // 全局日志序号（从 0 开始）
	Kind  EntryKind
	Key   string
	Value string
}

// RejectReason 是批被拒绝的可区分原因。
type RejectReason string

const (
	// ReasonEmptyKey 键为空字符串。
	ReasonEmptyKey RejectReason = "empty_key"
	// ReasonInvalidOp 操作类型非法（既非 put 也非 delete）。
	ReasonInvalidOp RejectReason = "invalid_op"
	// ReasonTooManyKeys 批结束后存活键数超过上限。
	ReasonTooManyKeys RejectReason = "too_many_live_keys"
)

// BatchError 描述一批写入被拒绝的原因与位置。
// 被拒绝的批不会改变当前表与已产生的日志。
type BatchError struct {
	Reason RejectReason
	Index  int // 触发拒绝的写入在批内的下标（ReasonTooManyKeys 时为 -1）
	Key    string
	Op     Op
	Count  int // 批结束后存活键数（ReasonTooManyKeys 时有效）
	Limit  int // 存活键上限
}

func (e *BatchError) Error() string {
	switch e.Reason {
	case ReasonEmptyKey:
		return fmt.Sprintf("changelog: batch rejected at index %d: empty key", e.Index)
	case ReasonInvalidOp:
		return fmt.Sprintf("changelog: batch rejected at index %d (key=%q): invalid op %q", e.Index, e.Key, e.Op)
	case ReasonTooManyKeys:
		return fmt.Sprintf("changelog: batch rejected: live key count %d exceeds limit %d", e.Count, e.Limit)
	default:
		return fmt.Sprintf("changelog: batch rejected: %s", e.Reason)
	}
}

// Coordinator 是并发安全的折叠器：维护当前表与撤回式变更日志。
// 每批 Apply 整体原子：并发只读得到的表与日志逐字段一致且来自同一时刻。
type Coordinator struct {
	mu          sync.RWMutex
	table       map[string]string
	log         []Entry
	maxLiveKeys int // <=0 表示不限
}

// New 创建折叠器，maxLiveKeys 为批结束后存活键数上限（<=0 表示不限）。
func New(maxLiveKeys int) *Coordinator {
	return &Coordinator{
		table:       make(map[string]string),
		maxLiveKeys: maxLiveKeys,
	}
}

// Apply 原子地应用一批写入，返回本批折叠后追加的日志条目（按规则排序）。
// 批非法时返回 *BatchError，且不改变当前表与已产生的日志。
//
// 折叠只看每个被触及键批前/批后的净状态：
//   - 不存在→存在 v：           upsert(k, v)
//   - 存在 old→不存在：          retract(k, old)
//   - 存在 old→存在 v，old != v：retract(k, old) 后 upsert(k, v)
//   - 状态（含值）相同：         不输出
//
// 条目中的 Seq 在提交时按全局日志顺序分配。
func (c *Coordinator) Apply(batch []Write) ([]Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 第一遍：静态校验。任何非法输入都在触碰状态之前拒绝整批。
	for i, w := range batch {
		if w.Key == "" {
			return nil, &BatchError{Reason: ReasonEmptyKey, Index: i, Op: w.Op}
		}
		if w.Op != OpPut && w.Op != OpDelete {
			return nil, &BatchError{Reason: ReasonInvalidOp, Index: i, Key: w.Key, Op: w.Op}
		}
	}

	// 第二遍：在独立草稿上模拟整批，记录每个键首次出现时的批前状态与首次出现次序。
	scratch := make(map[string]string, len(c.table)+len(batch))
	for k, v := range c.table {
		scratch[k] = v
	}

	order := make([]string, 0, len(batch)) // 键的首次出现顺序
	seen := make(map[string]struct{}, len(batch))
	beforeVal := make(map[string]string, len(batch))
	beforeExisted := make(map[string]bool, len(batch))

	for _, w := range batch {
		if _, ok := seen[w.Key]; !ok {
			seen[w.Key] = struct{}{}
			order = append(order, w.Key)
			beforeVal[w.Key], beforeExisted[w.Key] = scratch[w.Key]
		}
		if w.Op == OpPut {
			scratch[w.Key] = w.Value
		} else {
			delete(scratch, w.Key)
		}
	}

	// 批结束后存活键数超限，整体拒绝。
	if c.maxLiveKeys > 0 && len(scratch) > c.maxLiveKeys {
		return nil, &BatchError{
			Reason: ReasonTooManyKeys,
			Index:  -1,
			Count:  len(scratch),
			Limit:  c.maxLiveKeys,
		}
	}

	// 第三遍：按首次出现顺序逐键比较批前/批后净状态，折叠出日志条目。
	entries := make([]Entry, 0, len(order)*2)
	seq := len(c.log)
	for _, k := range order {
		oldVal, oldExisted := beforeVal[k], beforeExisted[k]
		newVal, newExisted := scratch[k]
		switch {
		case !oldExisted && newExisted:
			entries = append(entries, Entry{Seq: seq, Kind: KindUpsert, Key: k, Value: newVal})
			seq++
		case oldExisted && !newExisted:
			entries = append(entries, Entry{Seq: seq, Kind: KindRetract, Key: k, Value: oldVal})
			seq++
		case oldExisted && newExisted && oldVal != newVal:
			// 先撤回旧值，再写入新值。
			entries = append(entries, Entry{Seq: seq, Kind: KindRetract, Key: k, Value: oldVal})
			seq++
			entries = append(entries, Entry{Seq: seq, Kind: KindUpsert, Key: k, Value: newVal})
			seq++
		}
		// 状态相同（含删除不存在的键、写入相同的值）：不输出。
	}

	// 原子提交：替换表、追加日志。
	c.table = scratch
	c.log = append(c.log, entries...)

	return entries, nil
}

// Snapshot 返回当前表的深拷贝与日志的深拷贝，保证两者来自同一原子时刻。
func (c *Coordinator) Snapshot() (map[string]string, []Entry) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	table := make(map[string]string, len(c.table))
	for k, v := range c.table {
		table[k] = v
	}
	log := make([]Entry, len(c.log))
	copy(log, c.log)
	return table, log
}

// LiveCount 返回当前存活键数。
func (c *Coordinator) LiveCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.table)
}
