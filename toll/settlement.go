package toll

import (
	"fmt"
	"sort"
	"time"
)

type waypoint struct {
	gantry string
	t      time.Time
}

// inferPath 依据入口、出口与已收到的有效中间记录推定计费路径。
// 依次经过各途径点;每段取总费用最低、并列时门架序列字典序最小的简单路径。
// 每条路段按"经过该段起点门架时刻"对应的车型计费:有记录的门架取记录时刻,
// 无记录的门架继承所在区间起点途径点的时刻。
func inferPath(n *Network, v *Vehicle, j *Journey) ([]string, int64, error) {
	wps := []waypoint{{gantry: j.Entry.Gantry, t: j.Entry.Time}}
	recs := make([]*GantryRecord, 0, len(j.Records))
	for _, r := range j.Records {
		if r.Status != StatusValid {
			continue
		}
		if r.Time.Before(j.Entry.Time) || r.Time.After(j.Exit.Time) {
			continue
		}
		recs = append(recs, r)
	}
	sort.SliceStable(recs, func(a, b int) bool {
		if !recs[a].Time.Equal(recs[b].Time) {
			return recs[a].Time.Before(recs[b].Time)
		}
		return recs[a].Seq < recs[b].Seq
	})
	for _, r := range recs {
		wps = append(wps, waypoint{gantry: r.Gantry, t: r.Time})
	}
	wps = append(wps, waypoint{gantry: j.Exit.Gantry, t: j.Exit.Time})

	var full []string
	var total int64
	for i := 0; i+1 < len(wps); i++ {
		typ := v.TypeAt(wps[i].t)
		weight := func(seg *Segment) int64 {
			return seg.Distance * seg.Rates[typ]
		}
		p, c, ok := n.shortestPath(wps[i].gantry, wps[i+1].gantry, weight)
		if !ok {
			return nil, 0, fmt.Errorf("%w: %s -> %s", ErrPathUnreachable, wps[i].gantry, wps[i+1].gantry)
		}
		if i == 0 {
			full = append(full, p...)
		} else {
			full = append(full, p[1:]...)
		}
		total += c
	}
	return full, total, nil
}

// ledgerFor 返回(必要时创建)车辆某时刻所属自然月的台账。
func (s *Service) ledgerFor(vehicleID string, t time.Time) *MonthLedger {
	lt := t.In(s.cfg.Location)
	key := MonthKey{VehicleID: vehicleID, Year: lt.Year(), Month: lt.Month()}
	l, ok := s.ledgers[key]
	if !ok {
		l = &MonthLedger{}
		s.ledgers[key] = l
	}
	return l
}

// applyDelta 把行程费用与实收的差额落成一笔调整:
// 差额为正产生收取(结算/补扣,受月度封顶约束),为负产生退款(不使当月实收低于零)。
func (s *Service) applyDelta(j *Journey, now time.Time, initial bool, feeBefore int64, reason string) *Adjustment {
	delta := j.Fee - j.Received
	if delta == 0 {
		return nil
	}
	ledger := s.ledgerFor(j.Vehicle.ID, j.Exit.Time)
	adj := &Adjustment{
		Time:      now,
		FeeBefore: feeBefore,
		FeeAfter:  j.Fee,
		Path:      append([]string(nil), j.Path...),
		Reason:    reason,
	}
	if delta > 0 {
		if initial {
			adj.Kind = AdjustCharge
		} else {
			adj.Kind = AdjustBackcharge
		}
		charge := delta
		if s.cfg.MonthlyCap > 0 {
			remain := s.cfg.MonthlyCap - ledger.Received
			if remain < 0 {
				remain = 0
			}
			if charge > remain {
				charge = remain
			}
		}
		adj.Amount = charge
		adj.CappedUncollected = delta - charge
		j.Received += charge
		ledger.Received += charge
		ledger.CappedUncollected += adj.CappedUncollected
	} else {
		adj.Kind = AdjustRefund
		refund := -delta
		if refund > ledger.Received {
			adj.RefundOverflow = refund - ledger.Received
			refund = ledger.Received
		}
		adj.Amount = refund
		j.Received -= refund
		ledger.Received -= refund
		ledger.RefundOverflow += adj.RefundOverflow
	}
	j.Adjustments = append(j.Adjustments, adj)
	return adj
}

// recompute 用当前全部有效记录重新推定路径与费用,并落下差额调整。
// 重算后不可达时保持原路径与金额,返回说明。
func (s *Service) recompute(j *Journey, now time.Time, reason string) (*Adjustment, string) {
	path, fee, err := inferPath(s.net, j.Vehicle, j)
	if err != nil {
		return nil, "重算后路径不可达,保持原路径与金额"
	}
	feeBefore := j.Fee
	j.Path = path
	j.Fee = fee
	fullReason := fmt.Sprintf("%s:费用 %d -> %d,路径 %v", reason, feeBefore, fee, path)
	return s.applyDelta(j, now, false, feeBefore, fullReason), ""
}
