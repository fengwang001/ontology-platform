// Package probe 维护冷链设备的阶梯温度读数、读数缺口与全局单调时钟。
package probe

import (
	"errors"
	"sync"
)

// 哨兵错误，budget/release 包复用。
var (
	ErrInvalid   = errors.New("invalid argument")
	ErrClock     = errors.New("clock moved backwards")
	ErrForbidden = errors.New("release permission required")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("already registered")
	ErrState     = errors.New("state mismatch")
	ErrSpoiled   = errors.New("unit spoiled")
	ErrQA        = errors.New("QA review permission required")
)

const (
	minTemp = -1_000_000
	maxTemp = 1_000_000
	maxNow  = 1_000_000_000
)

// seg 为一段左闭右开、逐分钟权重恒定的区间。
type seg struct {
	start int64
	end   int64 // 开放段 end==0 表示尚未闭合
	unit  int64 // 每分钟权重
	pref  int64 // 本段起点处的全局前缀累计（不含本段）
}

type device struct {
	has  bool
	segs []seg // 闭合分段，最后一个元素之外另有开放读数段 open
	open seg   // 最新读数对应的开放段（end==0）
}

// Store 保存全部设备读数与单调时钟。
type Store struct {
	mu      sync.RWMutex
	hi      int64
	delta   int64
	weight  int64
	gap     int64
	nowMax  int64
	devices map[string]*device
	touched int64
}

func validID(s string) bool { return s != "" }

func validNow(n int64) bool { return n >= 0 && n <= maxNow }

// New 构造读数存储：hi 温度上限，delta 轻度带宽（0.1℃ 整数，≥1），
// weight 重度权重（2..10），gap 读数缺口阈值 G（分钟，1..1e6）。
// 构造参数非法时 panic（配置错误属于编程错误）。
func New(hi, delta, weight, gap int64) *Store {
	if delta < 1 || weight < 2 || weight > 10 || gap < 1 || gap > 1_000_000 ||
		hi < minTemp || hi > maxTemp {
		panic(ErrInvalid)
	}
	return &Store{
		hi:      hi,
		delta:   delta,
		weight:  weight,
		gap:     gap,
		devices: make(map[string]*device),
	}
}

func (s *Store) minuteWeight(temp int64) int64 {
	switch {
	case temp <= s.hi:
		return 0
	case temp <= s.hi+s.delta:
		return 1
	default:
		return s.weight
	}
}

// Reading 为 device 追加一条读数。
func (s *Store) Reading(deviceName string, temp, now int64) error {
	if !validID(deviceName) || temp < minTemp || temp > maxTemp || !validNow(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.nowMax {
		return ErrClock
	}
	d := s.devices[deviceName]
	if d == nil {
		d = &device{}
		s.devices[deviceName] = d
	}
	if d.has {
		rs := d.open.start
		if now == rs {
			return ErrState // 相邻读数时刻相等（被拒绝不推进时钟）
		}
		// 闭合上一条读数的有效覆盖段。
		coverEnd := rs + s.gap
		if now < coverEnd {
			coverEnd = now
		}
		d.open.end = coverEnd
		p := d.open.pref + (coverEnd-d.open.start)*d.open.unit
		d.segs = append(d.segs, d.open)
		if coverEnd < now {
			d.segs = append(d.segs, seg{start: coverEnd, end: now, unit: s.weight, pref: p})
			p += (now - coverEnd) * s.weight
		}
		d.open = seg{start: now, unit: s.minuteWeight(temp), pref: p}
	} else {
		d.open = seg{start: now, unit: s.minuteWeight(temp)}
	}
	d.has = true
	s.nowMax = now
	return nil
}

// ReadingNoClock 为 device 追加读数，不重复校验/推进时钟。
// 供已完成时钟校验并串行化的上层（release.System）使用。
func (s *Store) ReadingNoClock(deviceName string, temp, now int64) error {
	if !validID(deviceName) || temp < minTemp || temp > maxTemp || !validNow(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.devices[deviceName]
	if d == nil {
		d = &device{}
		s.devices[deviceName] = d
	}
	if d.has {
		rs := d.open.start
		if now == rs {
			return ErrState
		}
		coverEnd := rs + s.gap
		if now < coverEnd {
			coverEnd = now
		}
		d.open.end = coverEnd
		p := d.open.pref + (coverEnd-d.open.start)*d.open.unit
		d.segs = append(d.segs, d.open)
		if coverEnd < now {
			d.segs = append(d.segs, seg{start: coverEnd, end: now, unit: s.weight, pref: p})
			p += (now - coverEnd) * s.weight
		}
		d.open = seg{start: now, unit: s.minuteWeight(temp), pref: p}
	} else {
		d.open = seg{start: now, unit: s.minuteWeight(temp)}
	}
	d.has = true
	return nil
}

// HasDevice 报告设备是否已登记且至少有一条读数。
func (s *Store) HasDevice(deviceName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.devices[deviceName]
	return d != nil && d.has
}

// searchSeg 返回满足 start <= p 的最大分段下标（segs + 尾部开放段统一编号）。
// 仅对 starts 做二分；每次比较计入 touched。
func (s *Store) searchSeg(d *device, p int64) int {
	n := len(d.segs)
	lo, hi := 0, n+1 // 编号 n 为开放段
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		s.touched++
		var st int64
		if mid < n {
			st = d.segs[mid].start
		} else {
			st = d.open.start
		}
		if st <= p {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

// prefixAt 返回设备自首条读数起 [t0,p) 的累计权重；p 早于首读数时 ok=false。
func (s *Store) prefixAt(d *device, p int64) (int64, bool) {
	if !d.has {
		return 0, false
	}
	t0 := d.open.start
	if len(d.segs) > 0 {
		t0 = d.segs[0].start
	}
	if p <= t0 {
		return 0, p == t0
	}
	i := s.searchSeg(d, p-1)
	var cum int64
	if i < len(d.segs) {
		sg := d.segs[i]
		end := sg.end
		if p < end {
			end = p
		}
		cum = sg.pref + (end-sg.start)*sg.unit
	} else {
		// 开放读数段：有效覆盖至多 G，其后按缺口（重度）。
		coverEnd := d.open.start + s.gap
		if p < coverEnd {
			coverEnd = p
		}
		cum = d.open.pref + (coverEnd-d.open.start)*d.open.unit
		if p > d.open.start+s.gap {
			cum += (p - (d.open.start + s.gap)) * s.weight
		}
	}
	return cum, true
}

// Weight 返回 [a,b) 各分钟权重之和；a 早于设备首读数时 ok=false。
func (s *Store) Weight(deviceName string, a, b int64) (sum int64, ok bool) {
	if a > b {
		return 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.devices[deviceName]
	if d == nil {
		return 0, false
	}
	ca, ok1 := s.prefixAt(d, a)
	cb, ok2 := s.prefixAt(d, b)
	if !ok1 || !ok2 {
		return 0, false
	}
	return cb - ca, true
}

// CrossAfter 返回使设备区间 [a,t) 累计权重首次达到 need 的最小整数 t（a<t≤cap）。
func (s *Store) CrossAfter(deviceName string, a, need, cap int64) (int64, bool) {
	if need <= 0 || cap <= a {
		return 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.devices[deviceName]
	if d == nil {
		return 0, false
	}
	base, ok := s.prefixAt(d, a)
	if !ok {
		return 0, false
	}
	if cum, _ := s.prefixAt(d, cap); cum-base < need {
		return 0, false
	}
	// 在 (a, cap] 上二分最小整数 t，使 prefixAt(t)-base >= need。
	lo, hi := a+1, cap
	for lo < hi {
		mid := int64(uint64(lo+hi) >> 1)
		cum, ok := s.prefixAt(d, mid)
		if !ok {
			return 0, false
		}
		if cum-base >= need {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, true
}

// CheckClock 校验时钟单调性并在合法时推进时钟。
func (s *Store) CheckClock(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) {
		return ErrInvalid
	}
	if now < s.nowMax {
		return ErrClock
	}
	s.nowMax = now
	return nil
}

// Before 只读报告 now 是否早于已接受操作的最大时刻（时钟回退预判，不推进时钟）。
func (s *Store) Before(now int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return now < s.nowMax
}

// Advance 在调用方已完成回退预判后推进单调时钟。
func (s *Store) Advance(now int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now > s.nowMax {
		s.nowMax = now
	}
}

// TouchCount 返回非导出 touched 计数器（测试证明用）。
func (s *Store) TouchCount() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.touched
}

// ResetTouch 清零 touched 计数器。
func (s *Store) ResetTouch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched = 0
}

// HeavyWeight 返回重度档权重 w（空档重度分钟复用该权重）。
func (s *Store) HeavyWeight() int64 { return s.weight }
