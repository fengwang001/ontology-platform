// Package diff 持有源表与目标表，按主键归并比较并提供原子提交。
package diff

import (
	"errors"
	"sort"
	"sync"

	"ontology/norm"
)

// 分类。
const (
	ClsMissing = 0 // 只在源
	ClsExtra   = 1 // 只在目标
	ClsEqual   = 2
	ClsChanged = 3
)

// 列位掩码，按 d、c 次序。
const (
	ColD uint8 = 1 << 0
	ColC uint8 = 1 << 1
)

// Change 种类。
const (
	KindInsert = 0
	KindUpdate = 1
	KindDelete = 2
)

var (
	// ErrInvalid 表示参数或行内取值越界。
	ErrInvalid = errors.New("diff: invalid argument")
)

// ResultRow 是 Compare 的单行结果。
type ResultRow struct {
	ID      int64
	Class   int
	Changed uint8
}

// Change 是提交给目标端的一项原子变更（plan 包构造）。
type Change struct {
	ID      int64
	Kind    int
	D       *int64
	C       []byte
	Mask    uint8 // update 时有效
	Version int64 // insert 恒为 0
}

// StaleError 记录最小失配 id。
type StaleError struct{ ID int64 }

func (e *StaleError) Error() string { return "diff: stale version" }

// T 是对账器。
type T struct {
	mu      sync.RWMutex
	cfg     norm.Cfg
	src     map[int64]norm.Row
	tgt     map[int64]norm.Row
	version map[int64]int64
}

// New 创建对账器。
func New(cfg norm.Cfg) *T {
	return &T{
		cfg:     cfg,
		src:     make(map[int64]norm.Row),
		tgt:     make(map[int64]norm.Row),
		version: make(map[int64]int64),
	}
}

// SrcPut 写入源行。
func (t *T) SrcPut(r norm.Row) error {
	if err := norm.CheckRow(r); err != nil {
		return ErrInvalid
	}
	row := norm.Row{ID: r.ID, D: clonePtr(r.D), C: cloneBytes(r.C)}
	t.mu.Lock()
	t.src[r.ID] = row
	t.mu.Unlock()
	return nil
}

// TgtPut 写入目标行并令版本 +1。
func (t *T) TgtPut(r norm.Row) error {
	if err := norm.CheckRow(r); err != nil {
		return ErrInvalid
	}
	row := norm.Row{ID: r.ID, D: clonePtr(r.D), C: cloneBytes(r.C)}
	t.mu.Lock()
	t.tgt[r.ID] = row
	t.version[r.ID]++
	t.mu.Unlock()
	return nil
}

// TgtDel 删除目标行并令版本 +1（墓碑保留版本，不归零）。
func (t *T) TgtDel(id int64) error {
	if id < 1 || id > norm.MaxID {
		return ErrInvalid
	}
	t.mu.Lock()
	delete(t.tgt, id)
	t.version[id]++
	t.mu.Unlock()
	return nil
}

// Compare 归并 [lo,hi) 内两侧行。
func (t *T) Compare(lo, hi int64) ([]ResultRow, error) {
	rows, _, err := t.compare(lo, hi)
	return rows, err
}

// CompareVisited 执行比较并额外返回两侧在范围内被访问的行数。
func (t *T) CompareVisited(lo, hi int64) ([]ResultRow, int, error) {
	return t.compare(lo, hi)
}

// Snapshot 返回 [lo,hi) 内源与目标归一化行的不可变拷贝及目标版本。
func (t *T) Snapshot(lo, hi int64) (src, tgt map[int64]norm.Row, ver map[int64]int64, err error) {
	if !validRange(lo, hi) {
		return nil, nil, nil, ErrInvalid
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.srcSnapshotLocked(lo, hi), t.tgtSnapshotLocked(lo, hi), t.verSnapshotLocked(lo, hi), nil
}

// CompareSnapshot 在同一把读锁内原子地返回比较结果与归一化快照，
// 保证 Plan 记录的分类、行值与目标版本来自同一线性化时刻。
func (t *T) CompareSnapshot(lo, hi int64) ([]ResultRow, map[int64]norm.Row, map[int64]norm.Row, map[int64]int64, error) {
	if !validRange(lo, hi) {
		return nil, nil, nil, nil, ErrInvalid
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	rows, _, err := t.compareLocked(lo, hi)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return rows, t.srcSnapshotLocked(lo, hi), t.tgtSnapshotLocked(lo, hi), t.verSnapshotLocked(lo, hi), nil
}

func (t *T) srcSnapshotLocked(lo, hi int64) map[int64]norm.Row {
	src := make(map[int64]norm.Row)
	for id, r := range t.src {
		if id >= lo && id < hi {
			src[id] = t.cfg.CvtRow(r)
		}
	}
	return src
}

func (t *T) tgtSnapshotLocked(lo, hi int64) map[int64]norm.Row {
	tgt := make(map[int64]norm.Row)
	for id, r := range t.tgt {
		if id >= lo && id < hi {
			tgt[id] = t.cfg.TgtRow(r)
		}
	}
	return tgt
}

func (t *T) verSnapshotLocked(lo, hi int64) map[int64]int64 {
	ver := make(map[int64]int64)
	for id := range t.tgt {
		if id >= lo && id < hi {
			ver[id] = t.version[id]
		}
	}
	return ver
}

// Commit 在单把写锁内整体校验并应用变更，任一失配整体拒绝。
func (t *T) Commit(changes []Change) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var staleID int64
	haveStale := false
	for _, ch := range changes {
		_, present := t.tgt[ch.ID]
		bad := false
		switch ch.Kind {
		case KindInsert:
			bad = present
		case KindUpdate, KindDelete:
			bad = !present || t.version[ch.ID] != ch.Version
		}
		if bad && (!haveStale || ch.ID < staleID) {
			staleID = ch.ID
			haveStale = true
		}
	}
	if haveStale {
		return &StaleError{ID: staleID}
	}
	for _, ch := range changes {
		switch ch.Kind {
		case KindInsert:
			t.tgt[ch.ID] = norm.Row{ID: ch.ID, D: clonePtr(ch.D), C: cloneBytes(ch.C)}
			t.version[ch.ID]++
		case KindUpdate:
			cur := t.tgt[ch.ID]
			if ch.Mask&ColD != 0 {
				cur.D = clonePtr(ch.D)
			}
			if ch.Mask&ColC != 0 {
				cur.C = cloneBytes(ch.C)
			}
			t.tgt[ch.ID] = cur
			t.version[ch.ID]++
		case KindDelete:
			delete(t.tgt, ch.ID)
			t.version[ch.ID]++
		}
	}
	return nil
}

// Version 返回某 id 的目标版本（含墓碑），以及当前是否存在。
func (t *T) Version(id int64) (v int64, exists bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, exists = t.tgt[id]
	return t.version[id], exists
}

func (t *T) compare(lo, hi int64) ([]ResultRow, int, error) {
	if !validRange(lo, hi) {
		return nil, 0, ErrInvalid
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.compareLocked(lo, hi)
}

func (t *T) compareLocked(lo, hi int64) ([]ResultRow, int, error) {
	srcIDs := idsInRange(t.src, lo, hi)
	tgtIDs := idsInRange(t.tgt, lo, hi)
	visited := len(srcIDs) + len(tgtIDs)
	rows := make([]ResultRow, 0, visited)
	i, j := 0, 0
	for i < len(srcIDs) || j < len(tgtIDs) {
		switch {
		case j == len(tgtIDs) || (i < len(srcIDs) && srcIDs[i] < tgtIDs[j]):
			rows = append(rows, ResultRow{ID: srcIDs[i], Class: ClsMissing})
			i++
		case i == len(srcIDs) || (j < len(tgtIDs) && tgtIDs[j] < srcIDs[i]):
			rows = append(rows, ResultRow{ID: tgtIDs[j], Class: ClsExtra})
			j++
		default:
			id := srcIDs[i]
			s := t.cfg.CvtRow(t.src[id])
			g := t.cfg.TgtRow(t.tgt[id])
			mask := uint8(0)
			if !ptrEqual(s.D, g.D) {
				mask |= ColD
			}
			if !bytesEqual(s.C, g.C) {
				mask |= ColC
			}
			cls := ClsEqual
			if mask != 0 {
				cls = ClsChanged
			}
			rows = append(rows, ResultRow{ID: id, Class: cls, Changed: mask})
			i++
			j++
		}
	}
	return rows, visited, nil
}

func idsInRange(m map[int64]norm.Row, lo, hi int64) []int64 {
	ids := make([]int64, 0, len(m))
	for id := range m {
		if id >= lo && id < hi {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	return ids
}

func validRange(lo, hi int64) bool {
	return lo >= 1 && hi <= norm.MaxID+1 && lo <= hi
}

func ptrEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func bytesEqual(a, b []byte) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func clonePtr(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
