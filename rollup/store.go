// Package rollup 实现带法律保全感知的分层保留与降采样时间序列存储。
//
// 三层：L0 原始点、L1 分钟聚合桶、L2 小时聚合桶。所有公开方法由单把
// 互斥锁串行化，等价于某个合法的全局串行顺序。
package rollup

import (
	"sync"

	"ontology/hold"
	"ontology/tier"
)

// Store 是单条时间序列的分层存储。
type Store struct {
	mu    sync.Mutex
	st    state
	now   int64
	a0    int64
	a1    int64
	a2    int64
	cap   int64
	Holds *hold.Registry
	// PanicHook 仅测试使用：在 Advance 副本收敛到指定阶段后被调用，
	// 若它 panic，则 Advance recover 后返回 ErrAborted 且状态回滚。
	PanicHook func(phase string)
}

// New 构造存储。A0<=A1<=A2 均为正毫秒，Cap 非负，否则构造失败。
func New(a0, a1, a2, capUnits int64, reg *hold.Registry) (*Store, error) {
	if a0 <= 0 || a1 <= 0 || a2 <= 0 || a0 > a1 || a1 > a2 || capUnits < 0 {
		return nil, tier.ErrInvalidArgument
	}
	if reg == nil {
		reg = hold.NewRegistry()
	}
	return &Store{
		st:    newState(),
		a0:    a0,
		a1:    a1,
		a2:    a2,
		cap:   capUnits,
		Holds: reg,
	}, nil
}

// Now 返回当前时钟（毫秒）。
func (s *Store) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Units 返回当前占用单位总数（<=Cap）。
func (s *Store) Units() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.units()
}

// Write 写入一个点。拒绝顺序：参数非法 -> 过期 -> 溢出 -> 容量。
func (s *Store) Write(ts, v int64) error {
	if ts < 0 || ts > tier.MaxClock || v < -tier.MaxValue || v > tier.MaxValue {
		return tier.ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	m, h := tier.MinuteOf(ts), tier.HourOf(ts)
	mf, mt := tier.MinuteRange(m)
	minuteHeld := s.Holds.IntersectsInterval(mf, mt)

	// 与任一生效保全相交的分钟，点一律落 L0；否则按年龄决定落层。
	if !minuteHeld {
		hourEnd := (h + 1) * tier.HourMS
		if age := s.now - hourEnd; age >= s.a2 {
			return tier.ErrExpired
		} else if age >= s.a1 {
			return s.writeToBucket(s.st.l2, h, v)
		}
		minuteEnd := (m + 1) * tier.MinuteMS
		if s.now-minuteEnd >= s.a0 {
			return s.writeToBucket(s.st.l1, m, v)
		}
	}

	// 落 L0：新增点占 1 单位。
	if s.st.units() >= s.cap {
		return tier.ErrCapacity
	}
	s.st.insertPoint(tier.Point{TS: ts, V: v})
	return nil
}

// writeToBucket 把点并入已有的 L1/L2 桶（不占容量）；桶不存在时先查容量再新建。
// 拒绝顺序：溢出 -> 容量。
func (s *Store) writeToBucket(buckets map[int64]tier.Bucket, key, v int64) error {
	b, exists := buckets[key]
	if !exists {
		if s.st.units() >= s.cap {
			return tier.ErrCapacity
		}
	}
	if err := b.AddPoint(v); err != nil {
		return err
	}
	buckets[key] = b
	return nil
}

// QueryResult 是范围查询结果。
type QueryResult struct {
	Count   int64
	Sum     int64
	Min     int64
	Max     int64
	Skipped int64
}

// Query 返回 [from,to) 的聚合。L0 点精确计入；L1/L2 桶仅整桶落在范围内才
// 并入，部分相交则整桶跳过并把其 Count 累加进 Skipped。
func (s *Store) Query(from, to int64) (QueryResult, error) {
	if from < 0 || to > tier.MaxClock || from >= to {
		return QueryResult{}, tier.ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	res := QueryResult{}
	acc := func(b tier.Bucket) {
		if res.Count == 0 {
			res.Min, res.Max = b.Min, b.Max
		} else {
			if b.Min < res.Min {
				res.Min = b.Min
			}
			if b.Max > res.Max {
				res.Max = b.Max
			}
		}
		res.Count += b.Count
		res.Sum += b.Sum
	}
	skip := func(b tier.Bucket) { res.Skipped += b.Count }
	whole := func(bf, bt int64) bool { return bf >= from && bt <= to }

	for _, p := range s.st.l0 {
		if p.TS >= from && p.TS < to {
			acc(tier.Bucket{Count: 1, Sum: p.V, Min: p.V, Max: p.V})
		}
	}
	for _, m := range sortedKeys(s.st.l1) {
		bf, bt := tier.MinuteRange(m)
		b := s.st.l1[m]
		if tier.IntervalsOverlap(bf, bt, from, to) {
			if whole(bf, bt) {
				acc(b)
			} else {
				skip(b)
			}
		}
	}
	for _, h := range sortedKeys(s.st.l2) {
		bf, bt := tier.HourRange(h)
		b := s.st.l2[h]
		if tier.IntervalsOverlap(bf, bt, from, to) {
			if whole(bf, bt) {
				acc(b)
			} else {
				skip(b)
			}
		}
	}
	return res, nil
}
