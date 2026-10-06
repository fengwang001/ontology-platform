package dr

import "fmt"

// Assess 由运营方对已结束事件（或窗口开始后取消的事件）发起考核。
// 要求所有已接受参与者在窗口与调整期内的数据齐全；任一参与者不满足则
// 整个考核被拒绝，报出第一个不满足的参与者与原因，事件状态不变。
// 考核成功后结果不可变。
func (s *System) Assess(now int64, eventID string) (*Assessment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	e := s.events[eventID]
	if e == nil {
		return nil, newErr(ErrKindEventState, "事件 %s 不存在", eventID)
	}
	st := e.state(now)
	if !(st == StateEnded || (st == StateCancelled && e.CancelAfterStart)) {
		return nil, newErr(ErrKindEventState, "事件 %s 状态为 %s，不允许考核", eventID, st)
	}
	adjustOffs, windowOffs := s.dayOffsets(e)
	all := make([]int64, 0, len(adjustOffs)+len(windowOffs))
	all = append(all, adjustOffs...)
	all = append(all, windowOffs...)
	dayStart := e.Params.Day * s.cfg.TicksPerDay
	participants := sortedKeys(e.commitments)
	for _, p := range participants {
		for i, off := range all {
			if _, ok := s.meterGet(p, dayStart+off); !ok {
				which := "调整期"
				if i >= len(adjustOffs) {
					which = "事件窗口"
				}
				return nil, &Error{
					Kind:        ErrKindIncomplete,
					Msg:         fmt.Sprintf("事件 %s 考核被拒绝：数据不齐全", eventID),
					Participant: p,
					Reason:      fmt.Sprintf("%s间隔 %d 用电数据缺失", which, dayStart+off),
				}
			}
		}
	}
	res := &Assessment{EventID: eventID}
	for _, p := range participants {
		res.Results = append(res.Results, s.settleParticipant(p, e, adjustOffs, windowOffs))
	}
	e.assessment = res
	e.Settled = true
	end := e.effectiveWindowEnd()
	for _, p := range participants {
		if s.watermark[p] < end {
			s.watermark[p] = end
		}
	}
	s.advance(now)
	return res, nil
}

// settleParticipant 计算一名参与者的考核结果。
func (s *System) settleParticipant(p string, e *Event, adjustOffs, windowOffs []int64) ParticipantResult {
	c := e.commitments[p]
	committed := c.Committed * float64(len(windowOffs)) / float64(e.Params.WindowIntervals)
	r := ParticipantResult{Participant: p, Committed: committed, LateWithdrawn: c.LateWithdrawn}
	if c.LateWithdrawn {
		s.finishSettlement(e, &r)
		return r
	}
	all := make([]int64, 0, len(adjustOffs)+len(windowOffs))
	all = append(all, adjustOffs...)
	all = append(all, windowOffs...)
	days := s.qualifyingDays(p, e, all)
	r.QualifyingDays = len(days)
	if len(days) < s.cfg.MinQualifyingDays {
		return r
	}
	r.Assessable = true
	means := s.baselineMeans(p, days, all)
	r.AdjRatio = s.adjustRatio(p, e, adjustOffs, means[:len(adjustOffs)])
	dayStart := e.Params.Day * s.cfg.TicksPerDay
	var reduction float64
	for j, off := range windowOffs {
		actual, _ := s.meterGet(p, dayStart+off)
		if d := means[len(adjustOffs)+j]*r.AdjRatio - actual; d > 0 {
			reduction += d
		}
	}
	r.Reduction = reduction
	s.finishSettlement(e, &r)
	return r
}

// finishSettlement 按履约率结算报酬与违约金，二者至多一项为正。
func (s *System) finishSettlement(e *Event, r *ParticipantResult) {
	if r.Committed > 0 {
		r.Ratio = r.Reduction / r.Committed
	}
	switch {
	case r.Ratio >= 1:
		r.Payment = r.Committed * e.Params.PayUnitPrice
	case r.Ratio >= e.Params.QualifiedRatio:
		r.Payment = r.Reduction * e.Params.PayUnitPrice
	default:
		r.Penalty = r.Committed * e.Params.PenaltyUnitPrice
	}
}
