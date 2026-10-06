package medsched

import "sort"

// PointStatus 是查询出的计划点状态。
type PointStatus int

const (
	StatusPending     PointStatus = iota + 1 // 待给
	StatusGivenOnTime                        // 已给按时
	StatusMadeUp                             // 已补给
	StatusRefused                            // 拒服
	StatusMissed                             // 漏给
	StatusVoid                               // 作废
)

func (st PointStatus) String() string {
	switch st {
	case StatusPending:
		return "待给"
	case StatusGivenOnTime:
		return "已给按时"
	case StatusMadeUp:
		return "已补给"
	case StatusRefused:
		return "拒服"
	case StatusMissed:
		return "漏给"
	case StatusVoid:
		return "作废"
	}
	return "未知"
}

// Point 是查询结果中的一个计划点。
type Point struct {
	OrderID string
	Planned int64
	Status  PointStatus
	Actual  int64 // 已处理时的实际时刻；未处理为 0
}

// unprocessedStatus 推导一个“尚无记录”的计划点状态。
// 停嘱时点的状态在停嘱时刻即冻结：planned+W < stop 的保持漏给，其余作废，
// 此后不再随查询 now 改变；在途医嘱才随 now 体现漏给。
func unprocessedStatus(planned, w, now, stoppedAt int64) PointStatus {
	if stoppedAt != 0 {
		if planned+w < stoppedAt {
			return StatusMissed
		}
		return StatusVoid
	}
	if now > planned+w {
		return StatusMissed
	}
	return StatusPending
}

// recordStatus 把处理记录映射为状态。
func recordStatus(k recordKind) PointStatus {
	switch k {
	case rkOnTime:
		return StatusGivenOnTime
	case rkRefused:
		return StatusRefused
	case rkMadeUp:
		return StatusMadeUp
	}
	return StatusVoid
}

// Query 查询某患者区间 [lo, hi] 内的计划点及状态，按 (计划时刻, 医嘱ID) 升序。
// 查询本身受单调时钟约束；结果仅由当前状态、区间与时钟决定。
func (s *System) Query(now int64, patient string, lo, hi int64) ([]Point, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTimeParam(now); err != nil {
		return nil, err
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	if patient == "" || lo < 0 || hi < lo {
		return nil, errf(ErrInvalidParam, "查询参数非法")
	}
	s.acceptClock(now)

	var out []Point
	for id, o := range s.orders {
		if o.patient != patient {
			continue
		}
		switch o.freq.Kind {
		case FreqDaily:
			dailyPointsBetween(o.dailyTimes, max64(lo, o.openedAt), hi, func(t int64) {
				out = append(out, classify(id, o, 0, t, now, s.w))
			})
		case FreqInterval:
			s.enumIntervalPoints(id, o, lo, hi, now, &out)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Planned != out[j].Planned {
			return out[i].Planned < out[j].Planned
		}
		return out[i].OrderID < out[j].OrderID
	})
	return out, nil
}

func classify(id string, o *order, gi int, t, now, w int64) Point {
	if idx := findIn(o.genRecords[gi], t); idx >= 0 {
		r := o.genRecords[gi][idx]
		return Point{OrderID: id, Planned: t, Status: recordStatus(r.kind), Actual: r.done}
	}
	return Point{OrderID: id, Planned: t, Status: unprocessedStatus(t, w, now, o.stoppedAt)}
}

// enumIntervalPoints 枚举固定间隔医嘱在 [lo, hi] 内的点。
// 已关闭代中，截止下标 cutoffK 之后且无记录的点一律为“作废”（补给重排使其后
// 未处理点全部作废），即便其时刻与新一代点重叠也保留为作废点，以字面符合规格。
// 与任意区间重叠的“代”数量为 O(1)（每代至少长一个 H，且只有紧邻区间边界的
// 常数个代需要展开），故开销不随历史点总数增长。
func (s *System) enumIntervalPoints(id string, o *order, lo, hi, now int64, out *[]Point) {
	h := o.freq.H
	for gi, g := range o.gens {
		genLo := lo
		if genLo < g.anchor {
			genLo = g.anchor
		}
		if genLo > hi {
			continue
		}
		klo, khi := intervalIndexRange(g.anchor, h, genLo, hi)
		for k := klo; k <= khi; k++ {
			t := intervalPoint(g.anchor, h, k)
			if t > hi {
				break
			}
			p := classify(id, o, gi, t, now, s.w)
			if g.cutoffK != maxInt64 && k > g.cutoffK && findIn(o.genRecords[gi], t) < 0 {
				// 重排导致的作废点。
				p.Status = StatusVoid
				p.Actual = 0
			}
			*out = append(*out, p)
		}
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
