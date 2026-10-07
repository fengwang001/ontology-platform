package temporalauth

// RegionVersion 是某地区默认时区定义的一个不可变版本。
// 迁移只能“追加新版本、向后生效”，绝不回溯改写历史版本。
type RegionVersion struct {
	ValidFrom Instant // 该版本开始生效的 UTC 时刻（含）
	ZoneID    string  // 生效期间该地区的默认时区 ID
}

// Region 是地区默认时区定义的版本链（按 ValidFrom 严格升序）。
type Region struct {
	ID       string
	Versions []RegionVersion
}

// EffectiveVersionAt 返回时刻 t 生效的地区版本下标（最新的 ValidFrom <= t）。
// ok 为 false 表示在 t 之前尚无任何版本，调用方必须以
// ErrRegionTimezoneUnresolved 失败，而不是猜测某一版本。
//
// 采用二分查找，复杂度 O(log n)，不随累计版本数 n 线性增长。
func (r *Region) EffectiveVersionAt(t Instant) (idx int, ok bool) {
	n := len(r.Versions)
	if n == 0 || t < r.Versions[0].ValidFrom {
		return 0, false
	}
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if r.Versions[mid].ValidFrom <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1, true
}

// CurrentVersion 返回当前最新版本下标。
func (r *Region) CurrentVersion() (idx int, ok bool) {
	if len(r.Versions) == 0 {
		return 0, false
	}
	return len(r.Versions) - 1, true
}

// ValidateVersionChain 校验版本链：ValidFrom 必须严格升序。
func (r *Region) ValidateVersionChain() error {
	for i := 1; i < len(r.Versions); i++ {
		if r.Versions[i].ValidFrom <= r.Versions[i-1].ValidFrom {
			return &AuthError{Code: ErrRegionTimezoneUnresolved}
		}
	}
	return nil
}
