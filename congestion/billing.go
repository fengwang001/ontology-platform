package congestion

import (
	"math"
	"sort"
	"time"
)

func dayKey(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02")
}

func inWindow(t time.Time, z *Zone, loc *time.Location) bool {
	l := t.In(loc)
	m := l.Hour()*60 + l.Minute()
	return m >= z.StartHHMM && m < z.EndHHMM
}

// recalcInput 为按日全量重算的纯输入。
type recalcInput struct {
	day     string
	cfg     Config
	zones   *registry
	entries []RawEntry      // 该车该自然日（配置时区）的全部进入，按 Seq 升序
	quals   []Qualification // 该车全部资格（按该日时刻过滤）
}

// recompute 为纯函数：输入当日全部事件与全部资格，结果与资格何时登记无关。
//
// 语义：
//   - 进入内层同时视为进入全部外层；候选登记顺序为“触达区域本身优先，
//     外层按区域 ID 字典序”，保证同分钟多事件可精确复现。
//   - 仅收费时段（左闭右开）内、当日首次的进入产生 ChargeLine。
//   - 资格适用优先级：残障 > 新能源 > 居民；被压过的资格不留痕。
//   - 居民减免后按最小货币单位向上取整；日封顶在折扣之后逐笔判定。
func recompute(in recalcInput) (payable int64, lines []ChargeLine) {
	loc := in.cfg.Location
	entered := map[string]bool{}
	var total int64

	for _, e := range in.entries {
		if dayKey(e.At, loc) != in.day {
			continue
		}
		candidates := make([]string, 0, 4)
		candidates = append(candidates, e.ZoneID)
		anc := in.zones.ancestors(e.ZoneID)
		sort.Strings(anc)
		candidates = append(candidates, anc...)

		for _, zid := range candidates {
			z, ok := in.zones.get(zid)
			if !ok {
				continue
			}
			if !inWindow(e.At, z, loc) {
				continue // 时段外：不计费，也不登记
			}
			if entered[zid] {
				continue // 当日再次进入不重复计费
			}
			entered[zid] = true

			gross := z.DailyFee
			net, reason := applyQuals(in.quals, zid, e.At, gross, in.cfg.DiscountBasis)
			line := ChargeLine{Seq: e.Seq, ZoneID: zid, At: e.At, Gross: gross}
			if total >= in.cfg.DailyCap {
				line.Payable = 0
				line.Reason = "capped:daily_cap_already_reached"
			} else if total+net > in.cfg.DailyCap {
				line.Payable = in.cfg.DailyCap - total
				total = in.cfg.DailyCap
				if reason != "" {
					line.Reason = "capped_at:" + reason
				} else {
					line.Reason = "capped:daily_cap"
				}
			} else {
				line.Payable = net
				line.Reason = reason
				total += net
			}
			if line.Reason == "" {
				line.Reason = "full_fee"
			}
			lines = append(lines, line)
		}
	}
	return total, lines
}

// applyQuals 按优先级在进入时刻挑选唯一适用资格，返回折后金额与依据。
func applyQuals(quals []Qualification, zoneID string, at time.Time, gross int64, basis int64) (int64, string) {
	var resident *Qualification
	var disabled, newEnergy bool
	for i := range quals {
		q := &quals[i]
		if !q.covers(at) {
			continue
		}
		switch q.Kind {
		case Disabled:
			disabled = true
		case NewEnergy:
			newEnergy = true
		case Resident:
			if q.ZoneID == zoneID && resident == nil {
				resident = q
			}
		}
	}
	if disabled {
		return 0, "exempt:disabled"
	}
	if newEnergy {
		return 0, "exempt:new_energy"
	}
	if resident != nil {
		relief := ceilDiv(gross*resident.Discount, basis)
		net := gross - relief
		if net < 0 {
			net = 0
		}
		return net, "resident_discount"
	}
	return gross, ""
}

func (q Qualification) covers(t time.Time) bool {
	return !t.Before(q.Start) && t.Before(q.End)
}

// ceilDiv 向上取整除法（分母为正）。
func ceilDiv(a, b int64) int64 {
	if b <= 0 {
		return 0
	}
	return int64(math.Ceil(float64(a) / float64(b)))
}
