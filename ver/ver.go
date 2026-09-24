// Package ver 维护单 Key 的版本序列：按 ValidFrom 有序、同 ValidFrom 覆盖、
// 墓碑、二分 AS OF 定位。不依赖其他包。
package ver

import "sort"

// Version 是一个版本；Tombstone 为真表示墓碑（生效区间内该 Key 无值）。
type Version struct {
	ValidFrom int64
	Value     string
	Tombstone bool
}

// Store 是单 Key 的版本序列，按 ValidFrom 升序。非并发安全，由上层串行化。
type Store struct {
	versions []Version
	checked  int // 最近一次 AsOf 检查过的版本个数（非导出，不进公开接口）
}

// Upsert 写入一个版本；同 ValidFrom 覆盖旧版本或墓碑。
func (s *Store) Upsert(validFrom int64, value string) {
	s.put(Version{ValidFrom: validFrom, Value: value})
}

// Delete 写入一个墓碑版本；同 ValidFrom 覆盖。
func (s *Store) Delete(validFrom int64) { s.put(Version{ValidFrom: validFrom, Tombstone: true}) }

func (s *Store) put(v Version) {
	vs := s.versions
	i := sort.Search(len(vs), func(i int) bool { return vs[i].ValidFrom >= v.ValidFrom })
	if i < len(vs) && vs[i].ValidFrom == v.ValidFrom {
		vs[i] = v
		return
	}
	vs = append(vs, Version{})
	copy(vs[i+1:], vs[i:])
	vs[i] = v
	s.versions = vs
}

// AsOf 返回 ValidFrom <= ts 中 ValidFrom 最大的版本；不存在或是墓碑时 found=false。
// 二分定位，检查个数记入非导出计数器。
func (s *Store) AsOf(ts int64) (value string, found bool) {
	vs := s.versions
	lo, hi := 0, len(vs)
	s.checked = 0
	for lo < hi { // 找最后一个 ValidFrom <= ts 的位置（开区间写法）
		mid := int(uint(lo+hi) >> 1)
		s.checked++
		if vs[mid].ValidFrom <= ts {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return "", false
	}
	if v := vs[lo-1]; !v.Tombstone {
		return v.Value, true
	}
	return "", false
}

// CheckedWithin 只回答「最近一次 AsOf 的检查个数是否不超过 limit」，
// 不暴露计数器数值，供包外演示做上界判定。
func (s *Store) CheckedWithin(limit int) bool { return s.checked <= limit }
