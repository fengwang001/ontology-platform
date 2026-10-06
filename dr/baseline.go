package dr

import (
	"math/big"
	"sort"
)

// qualifyingDays 返回事件日之前最近的若干个资格日（至多 BaselineDays 个，
// 按从近到远顺序）。资格日须：与事件日同为工作日或休息日；未被该参与者
// 接受过的非取消事件占用；调整期与窗口时段用电数据齐全。
//
// 通过按参与者维护的有序"有数据日"索引直接定位候选日，开销只与
// 所需资格日数量及途中被排除的日期数相关，与历史数据总量无关。
func (s *System) qualifyingDays(participant string, e *Event) []int {
	rest := s.cfg.IsRestDay(e.Day)
	adjTod, _, endTod := s.windowTods(e)
	days := s.dataDays[participant]
	out := make([]int, 0, s.cfg.BaselineDays)
	for i := sort.SearchInts(days, e.Day) - 1; i >= 0 && len(out) < s.cfg.BaselineDays; i-- {
		d := days[i]
		s.Stats.QualifyCandidateDays++
		if s.cfg.IsRestDay(d) != rest {
			continue
		}
		if s.excluded[participant][d] > 0 {
			continue
		}
		if !s.hasComplete(participant, d, adjTod, endTod) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// adjustedBaseline 计算窗口内每个间隔的校正后基线。
// 基线为各资格日同时段用电量平均；同日校正比例为事件日调整期实际用电
// 与同期基线之比，裁剪到配置上下界（取等不裁剪），作用于每个窗口间隔。
// 调整期基线为零时校正比例取 1。
func (s *System) adjustedBaseline(participant string, e *Event, days []int) []*big.Rat {
	adjTod, startTod, endTod := s.windowTods(e)
	n := endTod - startTod
	cnt := big.NewRat(int64(len(days)), 1)

	base := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		sum := new(big.Rat)
		for _, d := range days {
			sum.Add(sum, big.NewRat(s.usageAt(participant, d*s.ipd+startTod+i), 1))
		}
		base[i] = sum.Quo(sum, cnt)
	}

	adjActual := new(big.Rat)
	adjBase := new(big.Rat)
	for t := adjTod; t < startTod; t++ {
		adjActual.Add(adjActual, big.NewRat(s.usageAt(participant, e.Day*s.ipd+t), 1))
		sum := new(big.Rat)
		for _, d := range days {
			sum.Add(sum, big.NewRat(s.usageAt(participant, d*s.ipd+t), 1))
		}
		adjBase.Add(adjBase, sum.Quo(sum, cnt))
	}

	ratio := big.NewRat(1, 1)
	if adjBase.Sign() != 0 {
		ratio = new(big.Rat).Quo(adjActual, adjBase)
	}
	if ratio.Cmp(s.adjMin) < 0 {
		ratio = new(big.Rat).Set(s.adjMin)
	} else if ratio.Cmp(s.adjMax) > 0 {
		ratio = new(big.Rat).Set(s.adjMax)
	}
	for i := range base {
		base[i].Mul(base[i], ratio)
	}
	return base
}
