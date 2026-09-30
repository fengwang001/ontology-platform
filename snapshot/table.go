package snapshot

import (
	"fmt"
	"sort"
	"sync"
)

// Snapshot 描述一个现存快照。
type Snapshot struct {
	ID        int64    // 快照 ID，自 1 起严格递增
	Timestamp int64    // 提交时间，严格递增
	Files     []string // 该快照引用的数据文件，升序
}

// Table 维护快照链、文件存储与引用计数。
// 所有方法均可被多个执行体并发调用。
type Table struct {
	mu        sync.RWMutex
	snapshots []*Snapshot     // 现存快照，按 ID 升序
	currentID int64           // 当前快照 ID
	lastTime  int64           // 最近一次提交时间
	usedNames map[string]bool // 历史使用过的全部文件名（永不复用）
	store     map[string]int  // 文件存储：文件名 -> 被现存快照引用的次数
	nextID    int64
}

// New 创建空表。
func New() *Table {
	return &Table{
		usedNames: make(map[string]bool),
		store:     make(map[string]int),
		nextID:    1,
	}
}

// Commit 以“当前快照文件集 - removed + added”产生新快照并设为当前快照。
// 任何校验失败都整体拒绝，不改变快照链、引用计数与文件存储。
func (t *Table) Commit(timestamp int64, added, removed []string) (Snapshot, error) {
	// 先完成全部校验，再应用任何变更，保证失败不留痕。
	if err := validateNames(added); err != nil {
		return Snapshot{}, err
	}
	if err := validateNames(removed); err != nil {
		return Snapshot{}, err
	}
	if overlap(added, removed) {
		return Snapshot{}, fmt.Errorf("%w: added and removed overlap", ErrInvalidArgument)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.snapshots) > 0 && timestamp <= t.lastTime {
		return Snapshot{}, fmt.Errorf("%w: got %d, last %d",
			ErrTimestampNotIncreasing, timestamp, t.lastTime)
	}

	current := t.currentFiles()
	currentSet := make(map[string]bool, len(current))
	for _, f := range current {
		currentSet[f] = true
	}
	for _, f := range removed {
		if !currentSet[f] {
			return Snapshot{}, fmt.Errorf("%w: %q", ErrRemoveNotInSnapshot, f)
		}
	}
	for _, f := range added {
		if t.usedNames[f] {
			return Snapshot{}, fmt.Errorf("%w: %q", ErrFileNameConflict, f)
		}
	}

	// 应用变更：新文件集 = 当前文件集 - removed + added。
	removedSet := make(map[string]bool, len(removed))
	for _, f := range removed {
		removedSet[f] = true
	}
	next := make([]string, 0, len(current)+len(added))
	for _, f := range current {
		if !removedSet[f] {
			next = append(next, f)
		}
	}
	next = append(next, added...)

	snap := &Snapshot{ID: t.nextID, Timestamp: timestamp, Files: sortedCopy(next)}
	t.snapshots = append(t.snapshots, snap)
	t.currentID = snap.ID
	t.lastTime = timestamp
	t.nextID++
	// 新快照引用其文件集中的每个文件（含从当前快照继承的文件），
	// 引用计数各加一；旧快照仍存活，不移除任何文件。
	for _, f := range snap.Files {
		t.store[f]++
	}
	for _, f := range added {
		t.usedNames[f] = true
	}
	return *snap, nil
}

// Expire 过期旧快照并删除不再被任何保留快照引用的文件。
// 一个现存快照被保留当且仅当满足以下三者之一（取并）：
//  1. 属于最新的 keepLast 个快照之一；
//  2. 其时间戳严格大于 threshold；
//  3. 它是当前快照。
//
// 返回被删除文件名（升序）。
func (t *Table) Expire(keepLast int, threshold int64) ([]string, error) {
	if keepLast < 0 {
		return nil, fmt.Errorf("%w: keepLast %d < 0", ErrInvalidArgument, keepLast)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	n := len(t.snapshots)
	retained := make([]*Snapshot, 0, n)
	expired := make([]*Snapshot, 0, n)
	for i, s := range t.snapshots {
		if i >= n-keepLast || s.Timestamp > threshold || s.ID == t.currentID {
			retained = append(retained, s)
		} else {
			expired = append(expired, s)
		}
	}

	t.snapshots = retained
	// 可删除文件 = 仅被本次过期快照引用、不被任何保留快照引用的文件。
	// 按引用计数判定：过期快照每引用一次减一，归零即从文件存储删除。
	var deleted []string
	for _, s := range expired {
		for _, f := range s.Files {
			t.store[f]--
			if t.store[f] == 0 {
				delete(t.store, f)
				deleted = append(deleted, f)
			}
		}
	}
	sort.Strings(deleted)
	return deleted, nil
}

// Snapshots 返回现存快照列表（按 ID 升序）的副本。
func (t *Table) Snapshots() []Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Snapshot, len(t.snapshots))
	for i, s := range t.snapshots {
		out[i] = Snapshot{ID: s.ID, Timestamp: s.Timestamp, Files: sortedCopy(s.Files)}
	}
	return out
}

// Files 返回文件存储中的全部文件名（升序）。
func (t *Table) Files() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.store))
	for f := range t.store {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// CheckConsistency 自检：
//   - 每个现存快照引用的文件都在文件存储中；
//   - 文件存储恰为全部现存快照文件集之并。
//
// 一致返回 nil，否则返回描述性错误。
func (t *Table) CheckConsistency() error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	union := make(map[string]int)
	for _, s := range t.snapshots {
		for _, f := range s.Files {
			if _, ok := t.store[f]; !ok {
				return fmt.Errorf("snapshot: file %q of snapshot %d missing from store", f, s.ID)
			}
			union[f]++
		}
	}
	if len(union) != len(t.store) {
		return fmt.Errorf("snapshot: store has %d files, union of snapshots has %d",
			len(t.store), len(union))
	}
	for f, cnt := range union {
		if t.store[f] != cnt {
			return fmt.Errorf("snapshot: refcount of %q is %d, want %d", f, t.store[f], cnt)
		}
	}
	return nil
}

// currentFiles 返回当前快照文件集；无快照时返回空。调用方须持有锁。
func (t *Table) currentFiles() []string {
	for _, s := range t.snapshots {
		if s.ID == t.currentID {
			return s.Files
		}
	}
	return nil
}

// validateNames 校验文件名列表：非空名且内部无重复。
func validateNames(names []string) error {
	seen := make(map[string]bool, len(names))
	for _, f := range names {
		if f == "" {
			return fmt.Errorf("%w: empty file name", ErrInvalidArgument)
		}
		if seen[f] {
			return fmt.Errorf("%w: duplicate file name %q", ErrInvalidArgument, f)
		}
		seen[f] = true
	}
	return nil
}

func overlap(a, b []string) bool {
	set := make(map[string]bool, len(a))
	for _, f := range a {
		set[f] = true
	}
	for _, f := range b {
		if set[f] {
			return true
		}
	}
	return false
}

func sortedCopy(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}
