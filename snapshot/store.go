// Package snapshot 实现表快照过期与可删除数据文件判定。
//
// 每次提交基于当前快照生成新快照，并按保留规则（最近 N 个、时间戳严格
// 大于阈值、当前快照三者取并）过期旧快照；仅被本次过期快照引用、且不被
// 任何保留快照引用的数据文件会被物理删除。
//
// 所有方法支持多执行体并发调用；任一非法输入都会被整体拒绝，
// 快照链、引用计数与文件存储均保持调用前状态（失败不留痕）。
package snapshot

import (
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"sync"
	"time"
)

// Snapshot 描述一个现存快照。
type Snapshot struct {
	// ID 为快照在表内的唯一序号，从 1 起严格递增。
	ID int64
	// CommitTime 为提交时间，全链严格递增。
	CommitTime time.Time
	// Files 为该快照引用的数据文件名集合（升序）。
	Files []string
}

// CommitResult 为一次提交+过期的结果。
type CommitResult struct {
	// NewSnapshotID 为本次产生的快照 ID。
	NewSnapshotID int64
	// Retained 为过期判定后现存的全部快照（按 ID 升序），新快照必在其中。
	Retained []Snapshot
	// ExpiredIDs 为本次被过期移除的快照 ID（升序）。
	ExpiredIDs []int64
	// DeletedFiles 为本次物理删除的数据文件名（升序）。
	DeletedFiles []string
}

// snap 为快照的内部表示。
type snap struct {
	id         int64
	commitTime time.Time
	files      []string // 升序、无重复
}

// Store 管理一张表的快照链、文件引用计数与文件存储。
// 零值不可直接使用，请通过 NewStore 构造。
type Store struct {
	mu sync.RWMutex

	// snaps 为现存快照，按 id 升序；最后一个即当前快照。
	snaps []*snap
	// nextID 为下一个快照 ID。
	nextID int64
	// everUsed 记录全生命周期曾经出现过的文件名（含已删除文件），
	// 用于保证“新增文件名永不复用”。
	everUsed map[string]struct{}
	// refCount 为每个现存文件被现存快照引用的次数。
	refCount map[string]int
	// storage 为文件存储：现存文件名集合。
	storage map[string]struct{}
}

var (
	loggerMu sync.RWMutex
	logger   = log.New(os.Stderr, "[snapshot] ", log.LstdFlags|log.Lmicroseconds)
)

// SetLogOutput 重定向判定日志输出位置，传 io.Discard 可关闭日志。
func SetLogOutput(w io.Writer) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	logger = log.New(w, "[snapshot] ", log.LstdFlags|log.Lmicroseconds)
}

func logf(format string, args ...any) {
	loggerMu.RLock()
	l := logger
	loggerMu.RUnlock()
	l.Output(2, fmt.Sprintf(format, args...))
}

// NewStore 创建一个空表的快照存储。
func NewStore() *Store {
	return &Store{
		nextID:   1,
		everUsed: make(map[string]struct{}),
		refCount: make(map[string]int),
		storage:  make(map[string]struct{}),
	}
}

// Commit 基于当前快照提交新快照，随后按规则过期旧快照并删除无引用文件。
//
// 新快照文件集 = 当前快照文件集 - removed + added。
// 约束：
//   - commitTime 必须严格晚于链上所有现存快照的提交时间；
//   - added 中的文件名在表的整个生命周期内不得出现过（永不复用），
//     added 内部不得重复，也不得与 removed 相交；
//   - removed 中的文件名必须全部包含在当前快照文件集内，内部不得重复；
//   - added/removed 均不得包含空文件名；retainCount 不得为负。
//
// 保留规则（三者取并）：提交后的一个现存快照被保留，当且仅当
//   - 它是最新的 retainCount 个快照之一（retainCount<=0 时该条件不保留任何快照），或
//   - 其提交时间严格大于 expireAfter，或
//   - 它是当前（最新）快照。
//
// 可删除文件：仅被本次过期快照引用、不被任何保留快照引用的文件，
// 物理删除后按文件名升序返回。任何参数违规都会整体拒绝，状态不变。
func (s *Store) Commit(commitTime time.Time, added, removed []string, retainCount int, expireAfter time.Time) (*CommitResult, error) {
	logf("Commit begin: commitTime=%s added=%v removed=%v retainCount=%d expireAfter=%s",
		commitTime.Format(time.RFC3339Nano), added, removed, retainCount,
		expireAfter.Format(time.RFC3339Nano))

	// ---- 阶段 1：纯校验，不修改任何状态（失败不留痕） ----
	if retainCount < 0 {
		logf("Commit reject: %v (retainCount=%d)", ErrInvalidArgument, retainCount)
		return nil, ErrInvalidArgument
	}

	addedSet, err := dedupNonEmpty(added, "added")
	if err != nil {
		logf("Commit reject on added: %v", err)
		return nil, err
	}
	removedSet, err := dedupNonEmpty(removed, "removed")
	if err != nil {
		logf("Commit reject on removed: %v", err)
		return nil, err
	}
	for name := range addedSet {
		if _, ok := removedSet[name]; ok {
			logf("Commit reject: %v (file %q in both added and removed)", ErrInvalidArgument, name)
			return nil, ErrInvalidArgument
		}
	}

	// 加锁后校验依赖内部状态的条件。
	s.mu.Lock()
	defer s.mu.Unlock()

	var current *snap
	if len(s.snaps) > 0 {
		current = s.snaps[len(s.snaps)-1]
		if !commitTime.After(current.commitTime) {
			logf("Commit reject: %v (commitTime=%s last=%s)",
				ErrTimeNotIncreasing,
				commitTime.Format(time.RFC3339Nano),
				current.commitTime.Format(time.RFC3339Nano))
			return nil, ErrTimeNotIncreasing
		}
	}

	currentSet := make(map[string]struct{})
	if current != nil {
		for _, name := range current.files {
			currentSet[name] = struct{}{}
		}
	}

	for _, name := range removed {
		if _, ok := currentSet[name]; !ok {
			logf("Commit reject: %v (file %q)", ErrFileNotInCurrent, name)
			return nil, ErrFileNotInCurrent
		}
	}
	for name := range addedSet {
		if _, ok := s.everUsed[name]; ok {
			logf("Commit reject: %v (file %q)", ErrFileNameReused, name)
			return nil, ErrFileNameReused
		}
	}

	// ---- 阶段 2：构造并安装新快照 ----
	newFiles := make([]string, 0, len(currentSet)-len(removedSet)+len(addedSet))
	for name := range currentSet {
		if _, drop := removedSet[name]; drop {
			continue
		}
		newFiles = append(newFiles, name)
	}
	for name := range addedSet {
		newFiles = append(newFiles, name)
	}
	slices.Sort(newFiles)

	newSnap := &snap{
		id:         s.nextID,
		commitTime: commitTime,
		files:      newFiles,
	}
	s.snaps = append(s.snaps, newSnap)

	for name := range addedSet {
		s.everUsed[name] = struct{}{}
		s.storage[name] = struct{}{}
	}
	// 引用计数只随“快照加入/移除”变化：新快照引用的每个文件 +1
	// （被替换的当前快照仍作为历史快照存在，其引用不消失）。
	for _, name := range newFiles {
		s.refCount[name]++
	}

	logf("Commit new snapshot: id=%d files=%v", newSnap.id, newSnap.files)

	// ---- 阶段 3：过期判定（三条件取并；当前快照无条件保留） ----
	candidates := s.snaps[:len(s.snaps)-1]
	retained := make([]*snap, 0, len(s.snaps))
	var expired []*snap
	for _, sp := range candidates {
		recent := retainCount > 0 && sp.id >= newSnap.id-int64(retainCount)+1
		fresh := sp.commitTime.After(expireAfter)
		switch {
		case recent && fresh:
			logf("snapshot %d retained: reason=latest-%d AND timestamp>threshold",
				sp.id, retainCount)
		case recent:
			logf("snapshot %d retained: reason=among latest %d snapshot(s)", sp.id, retainCount)
		case fresh:
			logf("snapshot %d retained: reason=timestamp %s > %s",
				sp.id, sp.commitTime.Format(time.RFC3339Nano),
				expireAfter.Format(time.RFC3339Nano))
		default:
			logf("snapshot %d expired: not in latest %d and timestamp %s <= %s",
				sp.id, retainCount, sp.commitTime.Format(time.RFC3339Nano),
				expireAfter.Format(time.RFC3339Nano))
			expired = append(expired, sp)
			continue
		}
		retained = append(retained, sp)
	}
	retained = append(retained, newSnap)
	logf("snapshot %d retained: reason=current snapshot (never expires)", newSnap.id)

	// ---- 阶段 4：移除过期快照、扣减引用并删除无引用文件 ----
	expiredIDs := make([]int64, 0, len(expired))
	for _, sp := range expired {
		expiredIDs = append(expiredIDs, sp.id)
		for _, name := range sp.files {
			s.refCount[name]--
		}
	}

	var deleted []string
	for _, sp := range expired {
		for _, name := range sp.files {
			if s.refCount[name] == 0 {
				deleted = append(deleted, name)
			}
		}
	}
	slices.Sort(deleted)
	deleted = slices.Compact(deleted)
	for _, name := range deleted {
		delete(s.refCount, name)
		delete(s.storage, name)
		logf("file %q deleted: referenced only by expired snapshots, retained reference count = 0", name)
	}

	s.snaps = retained
	s.nextID++

	result := &CommitResult{
		NewSnapshotID: newSnap.id,
		Retained:      exportSnaps(retained),
		ExpiredIDs:    expiredIDs,
		DeletedFiles:  deleted,
	}
	logf("Commit done: newSnapshotID=%d retainedIDs=%v expiredIDs=%v deletedFiles=%v",
		result.NewSnapshotID, snapIDs(result.Retained), result.ExpiredIDs, result.DeletedFiles)
	return result, nil
}

// dedupNonEmpty 将文件名切片校验为集合：不得包含空文件名、不得有重复。
func dedupNonEmpty(names []string, field string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			logf("reject: %v (empty file name in %s)", ErrInvalidArgument, field)
			return nil, ErrInvalidArgument
		}
		if _, dup := set[name]; dup {
			logf("reject: %v (duplicate file name %q in %s)", ErrInvalidArgument, name, field)
			return nil, ErrInvalidArgument
		}
		set[name] = struct{}{}
	}
	return set, nil
}

// GetSnapshot 返回 ID 对应的现存快照；不存在时返回 ErrSnapshotNotFound。
// 返回的是拷贝，调用方修改不会影响存储内部状态。
func (s *Store) GetSnapshot(id int64) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sp := range s.snaps {
		if sp.id == id {
			out := exportSnap(sp)
			logf("GetSnapshot id=%d hit: files=%v", id, out.Files)
			return &out, nil
		}
	}
	logf("GetSnapshot id=%d miss: %v", id, ErrSnapshotNotFound)
	return nil, ErrSnapshotNotFound
}

// ListSnapshots 返回全部现存快照（按 ID 升序）。
func (s *Store) ListSnapshots() []Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := exportSnaps(s.snaps)
	logf("ListSnapshots: ids=%v", snapIDs(out))
	return out
}

// CurrentSnapshotID 返回当前（最新）快照 ID；无快照时返回 0。
func (s *Store) CurrentSnapshotID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.snaps) == 0 {
		logf("CurrentSnapshotID: none (0)")
		return 0
	}
	id := s.snaps[len(s.snaps)-1].id
	logf("CurrentSnapshotID: %d", id)
	return id
}

// FileExists 判断数据文件是否存在于文件存储中。
func (s *Store) FileExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.storage[name]
	logf("FileExists %q: %v", name, ok)
	return ok
}

// ListFiles 返回文件存储中的全部文件名（升序），恰为现存快照文件集之并。
func (s *Store) ListFiles() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.storage))
	for name := range s.storage {
		out = append(out, name)
	}
	slices.Sort(out)
	logf("ListFiles: %v", out)
	return out
}

// CheckInvariants 执行自检：
//   - 每个现存快照引用的文件都在文件存储中；
//   - 文件存储恰为全部现存快照文件集之并；
//   - 引用计数与现存快照的实际引用次数一致。
//
// 返回 nil 表示自检通过；可与提交过期并发调用。
func (s *Store) CheckInvariants() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	union := make(map[string]int)
	var prevID int64
	var prevTime time.Time
	for i, sp := range s.snaps {
		if i > 0 && sp.id != prevID+1 {
			return fmt.Errorf("%w: snapshot id gap between %d and %d", ErrInvalidArgument, prevID, sp.id)
		}
		if i > 0 && !sp.commitTime.After(prevTime) {
			return fmt.Errorf("%w: snapshot %d time not strictly increasing", ErrInvalidArgument, sp.id)
		}
		if !slices.IsSorted(sp.files) {
			return fmt.Errorf("%w: snapshot %d files not sorted", ErrInvalidArgument, sp.id)
		}
		seen := make(map[string]struct{}, len(sp.files))
		for _, name := range sp.files {
			if _, dup := seen[name]; dup {
				return fmt.Errorf("%w: snapshot %d has duplicate file %q", ErrInvalidArgument, sp.id, name)
			}
			seen[name] = struct{}{}
			union[name]++
			if _, ok := s.storage[name]; !ok {
				return fmt.Errorf("%w: snapshot %d references missing file %q", ErrInvalidArgument, sp.id, name)
			}
		}
		prevID = sp.id
		prevTime = sp.commitTime
	}

	if len(union) != len(s.storage) {
		return fmt.Errorf("%w: storage size %d != union of snapshot files %d",
			ErrInvalidArgument, len(s.storage), len(union))
	}
	for name := range s.storage {
		if _, ok := union[name]; !ok {
			return fmt.Errorf("%w: orphan file %q in storage not referenced by any snapshot", ErrInvalidArgument, name)
		}
	}
	for name, want := range union {
		if got := s.refCount[name]; got != want {
			return fmt.Errorf("%w: refCount[%q]=%d, want %d", ErrInvalidArgument, name, got, want)
		}
	}
	logf("CheckInvariants OK: snapshots=%d files=%d", len(s.snaps), len(s.storage))
	return nil
}

func exportSnap(sp *snap) Snapshot {
	files := make([]string, len(sp.files))
	copy(files, sp.files)
	return Snapshot{ID: sp.id, CommitTime: sp.commitTime, Files: files}
}

func exportSnaps(in []*snap) []Snapshot {
	out := make([]Snapshot, len(in))
	for i, sp := range in {
		out[i] = exportSnap(sp)
	}
	return out
}

func snapIDs(in []Snapshot) []int64 {
	ids := make([]int64, len(in))
	for i, sp := range in {
		ids[i] = sp.ID
	}
	return ids
}
