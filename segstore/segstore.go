// Package segstore 实现不可变段存储：段的新建、删除标记、合并与物理释放。
// 所有共享状态由唯一一把互斥锁保护，任何并发调用都等价于某个串行顺序。
package segstore

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"sync"
)

var (
	// ErrInvalidParam 参数非法（now 越界、批大小/元素非法、重复段号等）。
	ErrInvalidParam = errors.New("segstore: invalid parameter")
	// ErrClock 时钟回退：now 小于已接受的最大 now。
	ErrClock = errors.New("segstore: clock regression")
	// ErrIDConflict 新建段中某 id 在当前视图中仍存活。
	ErrIDConflict = errors.New("segstore: id conflict")
	// ErrDocNotFound 删除目标在当前视图中不存活。
	ErrDocNotFound = errors.New("segstore: document not found")
	// ErrSegNotFound 合并目标段不在当前视图中。
	ErrSegNotFound = errors.New("segstore: segment not found")
)

// MaxNow 是所有带 now 参数操作中 now 的上界（毫秒）。
const MaxNow = int64(1_000_000_000_000)

func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

// Doc 是新建段时的输入文档。
type Doc struct {
	ID      string
	SortVal int64
}

// Segment 是不可变段；删除只写 delOp 标记，段内容本身永不改变。
// 段内文档按 (SortVal 升序, ID 字节序) 排序，下标即段内序号。
type Segment struct {
	num   int64
	ids   []string
	vals  []int64
	delOp []int64 // 0 表示存活，否则为删除该文档的操作号
}

// Num 返回全局段号。
func (s *Segment) Num() int64 { return s.num }

// Len 返回段内文档数。
func (s *Segment) Len() int { return len(s.ids) }

// ID 返回段内第 i 篇文档的 id。
func (s *Segment) ID(i int) string { return s.ids[i] }

// SortVal 返回段内第 i 篇文档的排序值。
func (s *Segment) SortVal(i int) int64 { return s.vals[i] }

// DelOp 返回段内第 i 篇文档的删除操作号，0 表示未被删除。
func (s *Segment) DelOp(i int) int64 { return s.delOp[i] }

// Pinner 由 pit 包实现，Store 在操作流水线中单向回调它。
// 其方法只在 Store 锁内被调用，实现者不得反向调用 Store。
type Pinner interface {
	// LandExpired 落地全部 exp <= now 的 PIT，返回它们释放引用的段号（可重复）。
	LandExpired(now int64) (dropped []int64)
	// Holds 报告段 seg 是否仍被某个未落地 PIT 持有。
	Holds(seg int64) bool
}

type loc struct {
	seg int64
	idx int
}

// Store 是全部共享状态的持有者：段表、当前视图、删除索引、时钟、
// 操作号与 Released 日志。
type Store struct {
	mu       sync.Mutex
	clock    int64 // 已接受的最大 now
	opSeq    int64 // 全局操作号，自 1 起
	segSeq   int64 // 全局段号，自 1 起
	segs     map[int64]*Segment
	view     map[int64]struct{}
	byID     map[string]loc // 当前视图中存活文档的位置
	released []int64
	pinner   Pinner
}

// NewStore 创建一个空存储。
func NewStore() *Store {
	return &Store{
		segs: make(map[int64]*Segment),
		view: make(map[int64]struct{}),
		byID: make(map[string]loc),
	}
}

// SetPinner 注册 PIT 管理器，须在并发使用前调用一次。
func (s *Store) SetPinner(p Pinner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pinner = p
}

// Released 返回已物理释放段号日志的副本（追加序）。
func (s *Store) Released() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.released)
}

// Tx 是一次操作在 Store 锁内的上下文，供 pit/search 包在 RunOp 中使用。
type Tx struct {
	store   *Store
	now     int64
	pending []int64 // 待检查释放的候选段号
}

// Now 返回本操作的 now。
func (tx *Tx) Now() int64 { return tx.now }

// View 返回当前视图的段号（升序）。
func (tx *Tx) View() []int64 {
	out := make([]int64, 0, len(tx.store.view))
	for n := range tx.store.view {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// NextOp 分配一个全局操作号。
func (tx *Tx) NextOp() int64 {
	tx.store.opSeq++
	return tx.store.opSeq
}

// Segment 按段号取段，不存在时返回 nil。
func (tx *Tx) Segment(num int64) *Segment { return tx.store.segs[num] }

// ReleaseCandidates 把段号登记为释放候选，操作结束时统一检查。
func (tx *Tx) ReleaseCandidates(nums ...int64) { tx.pending = append(tx.pending, nums...) }

// RunOp 执行一个带 now 的操作：时钟检查 → 落地过期 PIT 并推进时钟 → fn →
// 统一检查释放候选。落地与时钟推进在 fn 之前生效，即使 fn 返回错误也不回滚。
func (s *Store) RunOp(now int64, fn func(*Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return ErrClock
	}
	tx := &Tx{store: s, now: now}
	if s.pinner != nil {
		tx.pending = s.pinner.LandExpired(now)
	}
	s.clock = now
	err := fn(tx)
	s.releasePending(tx.pending)
	return err
}

// releasePending 对候选段做引用检查，失去全部引用的段物理释放，
// 段号按升序追加到 Released 日志。
func (s *Store) releasePending(candidates []int64) {
	if len(candidates) == 0 {
		return
	}
	seen := make(map[int64]struct{}, len(candidates))
	var out []int64
	for _, n := range candidates {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		if _, ok := s.segs[n]; !ok {
			continue
		}
		if _, ok := s.view[n]; ok {
			continue
		}
		if s.pinner != nil && s.pinner.Holds(n) {
			continue
		}
		delete(s.segs, n)
		out = append(out, n)
	}
	slices.Sort(out)
	s.released = append(s.released, out...)
}

// AddSegment 新建一个段并返回段号；docs 须为 1..10000 条、id 非空且批内不重复。
func (s *Store) AddSegment(now int64, docs []Doc) (int64, error) {
	if !validNow(now) || len(docs) < 1 || len(docs) > 10000 {
		return 0, ErrInvalidParam
	}
	seen := make(map[string]struct{}, len(docs))
	for _, d := range docs {
		if d.ID == "" {
			return 0, ErrInvalidParam
		}
		if _, dup := seen[d.ID]; dup {
			return 0, ErrInvalidParam
		}
		seen[d.ID] = struct{}{}
	}
	var num int64
	err := s.RunOp(now, func(tx *Tx) error {
		for _, d := range docs {
			if _, alive := s.byID[d.ID]; alive {
				return ErrIDConflict
			}
		}
		sorted := slices.Clone(docs)
		slices.SortFunc(sorted, func(a, b Doc) int {
			if a.SortVal != b.SortVal {
				return cmp.Compare(a.SortVal, b.SortVal)
			}
			return strings.Compare(a.ID, b.ID)
		})
		s.segSeq++
		num = s.segSeq
		seg := &Segment{num: num, delOp: make([]int64, len(sorted))}
		for _, d := range sorted {
			seg.ids = append(seg.ids, d.ID)
			seg.vals = append(seg.vals, d.SortVal)
		}
		s.segs[num] = seg
		s.view[num] = struct{}{}
		for i, d := range sorted {
			s.byID[d.ID] = loc{seg: num, idx: i}
		}
		tx.NextOp()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return num, nil
}

// Delete 给当前视图中存活的 id 打删除标记并记录本操作的操作号。
func (s *Store) Delete(now int64, id string) error {
	if !validNow(now) {
		return ErrInvalidParam
	}
	return s.RunOp(now, func(tx *Tx) error {
		l, ok := s.byID[id]
		if !ok {
			return ErrDocNotFound
		}
		op := tx.NextOp()
		seg := s.segs[l.seg]
		seg.delOp[l.idx] = op
		delete(s.byID, id)
		return nil
	})
}

// Merge 把 segs（2..10 个互不相同的段号）合并为一个新段，只保留此刻存活的
// 文档；旧段移出当前视图。若无存活文档则不建新段、不占段号，返回 0。
func (s *Store) Merge(now int64, segNums []int64) (int64, error) {
	if !validNow(now) || len(segNums) < 2 || len(segNums) > 10 {
		return 0, ErrInvalidParam
	}
	seen := make(map[int64]struct{}, len(segNums))
	for _, n := range segNums {
		if _, dup := seen[n]; dup {
			return 0, ErrInvalidParam
		}
		seen[n] = struct{}{}
	}
	var newNum int64
	err := s.RunOp(now, func(tx *Tx) error {
		for _, n := range segNums {
			if _, ok := s.view[n]; !ok {
				return ErrSegNotFound
			}
		}
		type kv struct {
			id  string
			val int64
		}
		var live []kv
		for _, n := range segNums {
			seg := s.segs[n]
			for i := 0; i < len(seg.ids); i++ {
				if seg.delOp[i] == 0 {
					live = append(live, kv{id: seg.ids[i], val: seg.vals[i]})
				}
			}
		}
		for _, n := range segNums {
			delete(s.view, n)
		}
		for _, d := range live {
			delete(s.byID, d.id)
		}
		if len(live) > 0 {
			slices.SortFunc(live, func(a, b kv) int {
				if a.val != b.val {
					return cmp.Compare(a.val, b.val)
				}
				return strings.Compare(a.id, b.id)
			})
			s.segSeq++
			newNum = s.segSeq
			seg := &Segment{num: newNum, delOp: make([]int64, len(live))}
			for _, d := range live {
				seg.ids = append(seg.ids, d.id)
				seg.vals = append(seg.vals, d.val)
			}
			s.segs[newNum] = seg
			s.view[newNum] = struct{}{}
			for i, d := range live {
				s.byID[d.id] = loc{seg: newNum, idx: i}
			}
		}
		tx.NextOp()
		tx.ReleaseCandidates(segNums...)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newNum, nil
}
