// Package tier 实现带法律保全的分层保留与降采样时间序列存储。
package tier

import (
	"errors"
	"sort"
	"sync"

	"ontology/hold"
	"ontology/rollup"
)

// 存储层公开错误，对应参数非法、过期、溢出、容量、时钟回退、中止回滚。
var (
	ErrInvalid  = errors.New("tier: invalid argument")
	ErrExpired  = errors.New("tier: point expired beyond L2 retention")
	ErrOverflow = errors.New("tier: aggregate sum overflows int64")
	ErrCapacity = errors.New("tier: capacity exhausted")
	ErrClock    = errors.New("tier: clock moved backwards")
	ErrAborted  = errors.New("tier: advance aborted and rolled back")
)

const maxTS = int64(10_000_000_000_000)
const maxV = int64(1_000_000_000_000)

// Result 是范围查询结果；Count==0 时 Min/Max 为 0。
type Result struct {
	Count   int64
	Sum     int64
	Min     int64
	Max     int64
	Skipped int64
}

// Store 是单个时间序列的三层存储：L0 原始点、L1 分钟桶、L2 小时桶。
type Store struct {
	now   int64
	a0    int64
	a1    int64
	a2    int64
	cap   int64
	l0    []rollup.Point
	l1    map[int64]rollup.Bucket
	l2    map[int64]rollup.Bucket
	holds *hold.Registry
	hook  func(step string)
	mu    sync.Mutex
}

// New 构造存储；要求 0 < A0 <= A1 <= A2 且 Cap > 0，否则返回 nil。
func New(a0, a1, a2, capacity int64) *Store {
	if a0 <= 0 || a1 <= 0 || a2 <= 0 || capacity <= 0 || a0 > a1 || a1 > a2 {
		return nil
	}
	return &Store{
		a0: a0, a1: a1, a2: a2, cap: capacity,
		l1:    map[int64]rollup.Bucket{},
		l2:    map[int64]rollup.Bucket{},
		holds: hold.NewRegistry(),
	}
}

// Write 按当前时钟判定落层。拒绝顺序：参数非法、过期、溢出、容量。
func (s *Store) Write(ts, v int64) error {
	if ts < 0 || ts > maxTS || v < -maxV || v > maxV {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.now
	m := rollup.MinuteOf(ts)
	h := rollup.HourOf(ts)
	mFrom, mTo := rollup.MinuteRange(m)

	// 分钟与任一生效保全相交：一律保留为 L0 原始点（保全优先）。
	held := s.holds.Intersects(mFrom, mTo)
	if !held {
		hEnd := (h + 1) * rollup.Hour
		if c-hEnd >= s.a2 {
			return ErrExpired
		}
		if c-hEnd >= s.a1 {
			return s.writeInto(s.l2, h, v)
		}
		if c-(m+1)*rollup.Minute >= s.a0 {
			return s.writeInto(s.l1, m, v)
		}
	}
	// 追加 L0 新点：先做溢出/容量预演，再真正改状态。
	if int64(len(s.l0)+len(s.l1)+len(s.l2))+1 > s.cap {
		return ErrCapacity
	}
	s.insertL0(rollup.Point{TS: ts, V: v})
	return nil
}

// writeInto 把值并入已有桶（不占容量）；拒绝顺序为先溢出、后容量。
func (s *Store) writeInto(layer map[int64]rollup.Bucket, key, v int64) error {
	b, exists := layer[key]
	merged, err := rollup.AddPoint(b, v)
	if err != nil {
		return ErrOverflow
	}
	if !exists && int64(len(s.l0)+len(s.l1)+len(s.l2))+1 > s.cap {
		return ErrCapacity
	}
	layer[key] = merged
	return nil
}

// insertL0 按 TS 有序插入（同 TS 追加在后），保证重放结果逐位一致。
func (s *Store) insertL0(p rollup.Point) {
	idx := sort.Search(len(s.l0), func(i int) bool { return s.l0[i].TS > p.TS })
	s.l0 = append(s.l0, rollup.Point{})
	copy(s.l0[idx+1:], s.l0[idx:])
	s.l0[idx] = p
}

func (s *Store) Holds() *hold.Registry { return s.holds }

// SetHook 注入在 Advance 每个阶段前调用的钩子；钩子 panic 会触发回滚。
func (s *Store) SetHook(hook func(step string)) {
	s.mu.Lock()
	s.hook = hook
	s.mu.Unlock()
}

// Units 返回当前总单位数（L0 点 + L1 桶 + L2 桶）。
func (s *Store) Units() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.l0) + len(s.l1) + len(s.l2)
}

func (s *Store) Clock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// state 是可整体替换的深快照，用于 Advance 的原子提交与回滚。
type state struct {
	l0 []rollup.Point
	l1 map[int64]rollup.Bucket
	l2 map[int64]rollup.Bucket
}

func (s *Store) snapshot() state {
	l0 := append([]rollup.Point(nil), s.l0...)
	l1 := make(map[int64]rollup.Bucket, len(s.l1))
	l2 := make(map[int64]rollup.Bucket, len(s.l2))
	for k, v := range s.l1 {
		l1[k] = v
	}
	for k, v := range s.l2 {
		l2[k] = v
	}
	return state{l0: l0, l1: l1, l2: l2}
}

func (s *Store) commit(st state) {
	s.l0, s.l1, s.l2 = st.l0, st.l1, st.l2
}
