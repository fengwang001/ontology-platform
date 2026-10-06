package inventory

// PerfStats 记录内部工作量计数，用于以可验证的方式证明
// 查询/预占开销不随累计条目数、也不随已过期但未触及的条目数增长。
type PerfStats struct {
	// SweptEntries 惰性归档的过期预占条目总数（每条预占至多被归档一次）。
	SweptEntries uint64
	// HotScanSteps 查询时扫描热表的步数（仅当查询时刻晚于已接受操作时刻才发生）。
	HotScanSteps uint64
	// ArchLookups 归档区二分查找次数。
	ArchLookups uint64
}

// expiryPax 归档区记录：某一到期时刻的占用人数。
type expiryPax struct {
	Expiry uint64
	Pax    int
}

// hotEnt 热表记录：一条尚未到期的预占在某舱位上的占用。
type hotEnt struct {
	expiry  uint64
	pax     int
	removed bool // 已出票或已取消，等待被归档指针越过
}

// classOcc 单个航段单个舱位等级的占用跟踪器。
//
// 设计要点：任一时刻 t 的占用是“当前已确认 + 尚未到期预占”的纯函数，
// 不依赖任何条目是否被操作触及。实现上利用“被接受操作的时刻单调不减、
// 预占时长固定”这两条性质：新预占的到期时刻单调不减，因此热表是一个
// 只追加的有序切片；归档指针单调前进，每条预占至多被归档一次，
// 均摊 O(1)。归档区只进不出（已归档的预占对未来一切操作都已过期，
// 不可能再被出票/取消），因此可以用前缀和做 O(log n) 的任意时刻查询，
// 而常见路径（t 不早于归档水位）走 O(1) 短路。
type classOcc struct {
	confirmed int // 已确认出票的人数（永不过期）

	hot      []hotEnt // 未到期预占，按到期时刻不减排列，只追加
	hotHead  int      // 归档水位：hot[hotHead:] 的到期时刻都 > sweptTo
	hotTotal int      // hot[hotHead:] 中未移除条目的总人数

	arch       []expiryPax // 已归档（对一切未来操作均已过期）的预占，按到期时刻不减排列
	archPrefix []int       // arch 的人数前缀和
	archTotal  int
}

// addHold 追加一条预占，返回其在热表中的下标。要求 expiry 不减。
func (c *classOcc) addHold(expiry uint64, pax int) int {
	c.hot = append(c.hot, hotEnt{expiry: expiry, pax: pax})
	c.hotTotal += pax
	return len(c.hot) - 1
}

// removeHot 将一条预占从热表标记移除（出票或取消时调用）。
func (c *classOcc) removeHot(idx, pax int) {
	c.hot[idx].removed = true
	c.hotTotal -= pax
}

// sweepTo 把到期时刻 <= target 的热表条目移入归档区。
// target 单调不减，每条预占至多被移动一次，均摊 O(1)。
func (c *classOcc) sweepTo(target uint64, stats *PerfStats) {
	i := c.hotHead
	for i < len(c.hot) && c.hot[i].expiry <= target {
		if !c.hot[i].removed {
			c.arch = append(c.arch, expiryPax{Expiry: c.hot[i].expiry, Pax: c.hot[i].pax})
			c.archTotal += c.hot[i].pax
			c.hotTotal -= c.hot[i].pax
			stats.SweptEntries++
		}
		i++
	}
	for len(c.archPrefix) < len(c.arch) {
		prev := 0
		if n := len(c.archPrefix); n > 0 {
			prev = c.archPrefix[n-1]
		}
		c.archPrefix = append(c.archPrefix, prev+c.arch[len(c.archPrefix)].Pax)
	}
	c.hotHead = i
}

// archSumLE 返回归档区中到期时刻 <= t 的总人数。O(log n) 二分。
func (c *classOcc) archSumLE(t uint64, stats *PerfStats) int {
	n := len(c.arch)
	if n == 0 || t < c.arch[0].Expiry {
		return 0
	}
	if t >= c.arch[n-1].Expiry {
		return c.archTotal
	}
	stats.ArchLookups++
	lo, hi := 0, n // 找第一个 Expiry > t 的下标
	for lo < hi {
		mid := (lo + hi) / 2
		if c.arch[mid].Expiry <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return c.archPrefix[lo-1]
}

// hotSumLE 返回热表中到期时刻 <= t 的未移除总人数。
// 仅在 t 晚于归档水位时被调用（少见路径），扫描热表前缀。
func (c *classOcc) hotSumLE(t uint64, stats *PerfStats) int {
	sum := 0
	for i := c.hotHead; i < len(c.hot) && c.hot[i].expiry <= t; i++ {
		stats.HotScanSteps++
		if !c.hot[i].removed {
			sum += c.hot[i].pax
		}
	}
	return sum
}

// occupied 返回时刻 t 计入可用数的占用人数。
// 调用前必须已把所属航段归档到 min(t, 最后接受操作时刻)。
// 当 t 不超过归档水位时，热表条目到期时刻都 > t，无需扫描热表。
func (c *classOcc) occupied(t uint64, sweptTo uint64, stats *PerfStats) int {
	hotLE := 0
	if t > sweptTo {
		hotLE = c.hotSumLE(t, stats)
	}
	return c.confirmed + c.hotTotal + c.archTotal - c.archSumLE(t, stats) - hotLE
}

// segment 一个航段：物理座位、超售上限与三个有序嵌套舱位等级。
type segment struct {
	id       string
	seats    int
	overbook int
	auth     [3]int // 授权量，必须满足 auth[0] >= auth[1] >= auth[2]，且 auth[0] <= seats+overbook
	occ      [3]classOcc
	sweptTo  uint64 // 归档水位（三个舱位共用）
}

// sweep 把航段归档水位推进到 target（target 小于等于水位时为空操作）。
func (s *segment) sweep(target uint64, stats *PerfStats) {
	if target <= s.sweptTo {
		return
	}
	s.sweptTo = target
	for k := range s.occ {
		s.occ[k].sweepTo(target, stats)
	}
}

// occupied 返回舱位 k 在时刻 t 的占用人数。
func (s *segment) occupied(k int, t uint64, stats *PerfStats) int {
	return s.occ[k].occupied(t, s.sweptTo, stats)
}

// avail 返回舱位 k 在时刻 t 的可用数：
// 该等级授权量减去该等级及所有更低等级的已占用数之和。可以为负。
func (s *segment) avail(k int, t uint64, stats *PerfStats) int {
	sum := 0
	for j := k; j < 3; j++ {
		sum += s.occupied(j, t, stats)
	}
	return s.auth[k] - sum
}

// sellable 报告舱位 k 在时刻 t 是否可售：
// 该等级及所有比它更高的等级的可用数都大于零。
func (s *segment) sellable(k int, t uint64, stats *PerfStats) bool {
	for j := 0; j <= k; j++ {
		if s.avail(j, t, stats) <= 0 {
			return false
		}
	}
	return true
}

// legFits 报告在舱位 k 预占 pax 人是否满足全部库存约束：
//  1. 指定等级可售（该等级及更高等级可用数都大于零）；
//  2. 指定等级可用数不小于 pax；
//  3. 最高等级可用数不小于 pax，以保证接受后
//     “已确认 + 未到期预占”之和不超过最高等级授权量。
func (s *segment) legFits(k, pax int, t uint64, stats *PerfStats) bool {
	if s.avail(0, t, stats) < pax {
		return false
	}
	for j := 1; j <= k; j++ {
		if s.avail(j, t, stats) <= 0 {
			return false
		}
	}
	return s.avail(k, t, stats) >= pax
}

// checkAuth 校验完整的三级授权量配置。
func checkAuth(seats, overbook int, auth [3]int) *Error {
	for _, a := range auth {
		if a < 0 {
			return paramErr("授权量不得为负")
		}
	}
	if auth[0] < auth[1] || auth[1] < auth[2] {
		return paramErr("授权量必须满足高等级不小于低等级")
	}
	if auth[0] > seats+overbook {
		return paramErr("最高等级授权量超过物理座位数与超售上限之和")
	}
	return nil
}

// checkAuthAdjust 校验把舱位 k 的授权量调整为 v 是否破坏嵌套次序。
// 允许调到低于当前已占用数（此时该等级及更低等级不可售，已有占用不受影响）。
func (s *segment) checkAuthAdjust(k, v int) *Error {
	if k > 0 && v > s.auth[k-1] {
		return paramErr("调整会破坏嵌套次序：高于上一等级")
	}
	if k < 2 && v < s.auth[k+1] {
		return paramErr("调整会破坏嵌套次序：低于下一等级")
	}
	if k == 0 && v > s.seats+s.overbook {
		return paramErr("最高等级授权量超过物理座位数与超售上限之和")
	}
	return nil
}
