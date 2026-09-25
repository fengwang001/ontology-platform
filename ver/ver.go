// Package ver 维护单 Key 的双时间戳版本历史：追加与四类查询。
// 不依赖其他包。并发安全由上层（hist）加锁保证，本包自身不加锁。
package ver

import "errors"

// ErrNotFound 表示所问的版本不存在（key 无版本，或没有满足时间条件的版本）。
var ErrNotFound = errors.New("ver: not found")

// Version 是一个版本三元组：值、事件时间 Ev（可乱序）、摄取时间 In（严格递增）。
type Version struct {
	Value string
	Ev    int64
	In    int64
}

// History 是单 Key 的版本历史。In 由上层保证严格递增，因此 vers 天然按 In 有序。
type History struct {
	vers    []Version
	maxEv   int // 当前 (Ev 最大, 并列取 In 最大) 版本的下标，Append 时 O(1) 维护
	checked int // 最近一次 LatestEvent 检查过的版本个数（非导出，仅包内测试/自检可读）
}

// Append 追加一个版本并维护 maxEv 指针。调用方须保证 v.In 严格大于已有最大 In。
// 由于 In 严格递增，新版本 In 必为最大，故 Ev 并列时新版本即为胜者：
// 更新条件 v.Ev >= 当前最大 Ev 同时覆盖了「Ev 更大」与「Ev 并列取 In 更大」。
func (h *History) Append(v Version) {
	h.vers = append(h.vers, v)
	if v.Ev >= h.vers[h.maxEv].Ev {
		h.maxEv = len(h.vers) - 1
	}
}

// Len 返回版本数。
func (h *History) Len() int {
	if h == nil {
		return 0
	}
	return len(h.vers)
}

// MaxIn 返回当前最大摄取时间（即最后到达者的 In）；无版本返回 0。
func (h *History) MaxIn() int64 {
	if h == nil || len(h.vers) == 0 {
		return 0
	}
	return h.vers[len(h.vers)-1].In
}

// LatestEvent 返回 Ev 最大的版本（Ev 并列取 In 最大）。由 maxEv 指针 O(1) 返回。
func (h *History) LatestEvent() (Version, error) {
	if h == nil || len(h.vers) == 0 {
		return Version{}, ErrNotFound
	}
	h.checked = 1 // 只检查指针指向的一个版本，不随版本总数增长
	return h.vers[h.maxEv], nil
}

// LatestIngest 返回 In 最大的版本（最后到达者）。
func (h *History) LatestIngest() (Version, error) {
	if h == nil || len(h.vers) == 0 {
		return Version{}, ErrNotFound
	}
	return h.vers[len(h.vers)-1], nil
}

// AtEvent 返回 Ev <= t 的版本中 Ev 最大者（并列取 In 最大）。
func (h *History) AtEvent(t int64) (Version, error) {
	if h == nil {
		return Version{}, ErrNotFound
	}
	best, ok := -1, false
	for i, v := range h.vers {
		if v.Ev <= t && (!ok || v.Ev > h.vers[best].Ev || (v.Ev == h.vers[best].Ev && v.In > h.vers[best].In)) {
			best, ok = i, true
		}
	}
	if !ok {
		return Version{}, ErrNotFound
	}
	return h.vers[best], nil
}

// AtIngest 返回 In <= t 的版本中 In 最大者。vers 按 In 升序，从尾部找第一个满足者。
func (h *History) AtIngest(t int64) (Version, error) {
	if h == nil {
		return Version{}, ErrNotFound
	}
	for i := len(h.vers) - 1; i >= 0; i-- {
		if h.vers[i].In <= t {
			return h.vers[i], nil
		}
	}
	return Version{}, ErrNotFound
}

// SelfCheck 在包内核验：LatestEvent 由指针 O(1) 返回（checked 不随 m 增长），
// 且与朴素扫描一致。checked 的数值不出包，只返回判定结果。
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		h := &History{}
		for i := 0; i < m; i++ { // 乱序 Ev
			h.Append(Version{Value: "v", Ev: int64((i*7919 + 13) % (m/2 + 1)), In: int64(i + 1)})
		}
		want, err := h.LatestEvent()
		if err != nil {
			return err
		}
		if h.checked > 1 {
			return errors.New("ver: LatestEvent 检查数随 m 增长")
		}
		naive := h.vers[0]
		for _, v := range h.vers[1:] {
			if v.Ev > naive.Ev || (v.Ev == naive.Ev && v.In > naive.In) {
				naive = v
			}
		}
		if want != naive {
			return errors.New("ver: LatestEvent 与朴素扫描不一致")
		}
	}
	return nil
}
