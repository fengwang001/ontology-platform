package tier

import (
	"sort"

	"ontology/rollup"
)

// Query 返回 [from, to) 的聚合结果。
// L0 点按 ts 精确计入；L1/L2 桶仅整桶落在区间内才并入，
// 部分相交的桶整体跳过，其 Count 计入 Skipped。
func (s *Store) Query(from, to int64) (Result, error) {
	if from < 0 || to > maxTS || from >= to {
		return Result{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var res Result
	var err error
	add := func(b rollup.Bucket) error {
		merged, err := rollup.Merge(rollup.Bucket{
			Count: res.Count, Sum: res.Sum, Min: res.Min, Max: res.Max,
		}, b)
		if err != nil {
			return err
		}
		res.Count, res.Sum, res.Min, res.Max = merged.Count, merged.Sum, merged.Min, merged.Max
		return nil
	}

	// L0：逐点精确匹配（同值重复点各自计入）。
	for _, p := range s.l0 {
		if p.TS >= from && p.TS < to {
			if err := add(rollup.Bucket{Count: 1, Sum: p.V, Min: p.V, Max: p.V}); err != nil {
				return Result{}, ErrOverflow
			}
		}
	}

	keys := make([]int64, 0, len(s.l1))
	for k := range s.l1 {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		b := s.l1[k]
		bs, be := rollup.MinuteRange(k)
		if err = switchOverlap(&res, b, bs, be, from, to, add); err != nil {
			return Result{}, err
		}
	}

	keys = keys[:0]
	for k := range s.l2 {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		b := s.l2[k]
		bs, be := rollup.HourRange(k)
		if err = switchOverlap(&res, b, bs, be, from, to, add); err != nil {
			return Result{}, err
		}
	}

	if res.Count == 0 {
		res.Min, res.Max = 0, 0
	}
	return res, nil
}

// switchOverlap 按桶区间与查询区间的关系决定整桶并入或跳过。
func switchOverlap(res *Result, b rollup.Bucket, bs, be, from, to int64,
	add func(rollup.Bucket) error) error {
	switch {
	case be <= from || bs >= to:
		// 不相交。
	case bs >= from && be <= to:
		if err := add(b); err != nil {
			return ErrOverflow
		}
	default:
		res.Skipped += b.Count
	}
	return nil
}
