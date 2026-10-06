package delivery

import "fmt"

func validExceptionType(et ExceptionType) bool {
	return et == ETUnreachable || et == ETWrongAddress || et == ETRejection
}

// ReportException 上报异常。拒收成立即进入无法送达处置。
func (s *System) ReportException(orderID string, at int64, et ExceptionType, evidenceID string, evidenceAt int64) (string, error) {
	if orderID == "" || at < 0 || !validExceptionType(et) {
		return "", &OpError{Code: ErrInvalidParam, Msg: "invalid report args"}
	}
	if et == ETRejection && evidenceID == "" {
		return "", &OpError{Code: ErrInvalidParam, Msg: "rejection requires evidence id"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return "", &OpError{Code: ErrOrderNotFound, Msg: "order not found: " + orderID}
	}
	if err := s.checkClock(at); err != nil {
		return "", err
	}
	s.settleAt(o, at)
	if o.view.Status != OSPicked {
		switch {
		case o.view.Status == OSDelivered:
			return "", &OpError{Code: ErrAlreadyDelivered, Msg: "already delivered"}
		case o.view.Status.Terminal():
			return "", &OpError{Code: ErrOrderTerminal, Msg: "order terminal"}
		case o.view.Status == OSException:
			return "", &OpError{Code: ErrActiveException, Msg: "active exception exists"}
		case o.view.Status == OSPlaced:
			return "", &OpError{Code: ErrNotPickedUp, Msg: "order not picked up"}
		default:
			return "", &OpError{Code: ErrWrongState, Msg: "order is not picked and deliverable"}
		}
	}
	if et == ETRejection && evidenceAt < at-s.params.EvidenceValidSeconds {
		return "", &OpError{Code: ErrInvalidEvidence, Msg: "evidence expired"}
	}
	s.commit(at)

	id := s.genID("e")
	ex := &exception{
		view: ExceptionView{
			ID:            id,
			OrderID:       orderID,
			Type:          et,
			Status:        ESActive,
			StartTime:     at,
			LastContactAt: -1,
			Deadline:      -1,
			EvidenceAt:    evidenceAt,
			EvidenceID:    evidenceID,
		},
		origX: o.view.AddressX,
		origY: o.view.AddressY,
	}
	if et == ETWrongAddress {
		ex.view.Deadline = at + s.params.CorrectionWindow
	}
	s.excs[id] = ex
	o.view.ActiveException = id
	o.view.Status = OSException

	if et == ETRejection {
		s.applyUndeliverable(o, ex, at, ESUndeliverable)
	}
	return id, nil
}

// lookupExc 仅做存在性解析（对应拒绝次序中的“异常不存在”）。
func (s *System) lookupExc(exceptionID string) (*order, *exception, error) {
	ex, ok := s.excs[exceptionID]
	if !ok {
		return nil, nil, &OpError{Code: ErrExceptionNotFound, Msg: "exception not found: " + exceptionID}
	}
	o := s.orders[ex.view.OrderID]
	if o == nil {
		return nil, nil, &OpError{Code: ErrOrderNotFound, Msg: "order not found: " + ex.view.OrderID}
	}
	return o, ex, nil
}

// checkActive 在通过类型检查之后校验异常仍进行中。
func (s *System) checkActive(o *order, ex *exception, exceptionID string) error {
	if o.view.ActiveException != exceptionID || ex.view.Status != ESActive {
		return &OpError{Code: ErrExceptionClosed, Msg: "exception not active"}
	}
	return nil
}

// RecordContact 记录一次联络尝试。判定开销不读历史明细，只看计数与上次时刻，O(1)。
func (s *System) RecordContact(exceptionID string, at int64) error {
	if exceptionID == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid contact args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ex, err := s.lookupExc(exceptionID)
	if err != nil {
		return err
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	s.settleAt(o, at)
	if ex.view.Type != ETUnreachable {
		return &OpError{Code: ErrTypeMismatch, Msg: "not an unreachable exception"}
	}
	if err := s.checkActive(o, ex, exceptionID); err != nil {
		return err
	}
	if ex.view.LastContactAt >= 0 && at-ex.view.LastContactAt < s.params.MinContactInterval {
		return &OpError{Code: ErrContactTooSoon,
			Msg: fmt.Sprintf("contact too soon: gap %d < %d", at-ex.view.LastContactAt, s.params.MinContactInterval)}
	}
	s.commit(at)
	ex.records = append(ex.records, ContactRecord{Time: at})
	ex.view.ContactCount++
	ex.view.LastContactAt = at
	return nil
}

// JudgeUndeliverable 判定联系不上无法送达；等待与联络双条件恰等满足。
func (s *System) conditionSatisfied(exceptionID string, at int64) (waitOK, contactOK bool) {
	ex := s.excs[exceptionID]
	return at-ex.view.StartTime >= s.params.MinWaitSeconds,
		ex.view.ContactCount >= s.params.MinContacts
}

func (s *System) JudgeUndeliverable(exceptionID string, at int64) error {
	if exceptionID == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid judge args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ex, err := s.lookupExc(exceptionID)
	if err != nil {
		return err
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	if ex.view.Type != ETUnreachable {
		return &OpError{Code: ErrTypeMismatch, Msg: "not an unreachable exception"}
	}
	if err := s.checkActive(o, ex, exceptionID); err != nil {
		return err
	}
	waitOK, contactOK := s.conditionSatisfied(exceptionID, at)
	// 两者都缺时报等待。
	if !waitOK {
		return &OpError{Code: ErrConditionWait, Msg: "minimum wait not reached"}
	}
	if !contactOK {
		return &OpError{Code: ErrConditionContact, Msg: "minimum contacts not reached"}
	}
	s.commit(at)
	s.applyUndeliverable(o, ex, at, ESUndeliverable)
	return nil
}

// UserRespond 用户在判定前回应：异常关闭，订单回到配送中；联络明细保留备查，
// 下次重新上报的异常计数从零开始。
func (s *System) UserRespond(exceptionID string, at int64) error {
	if exceptionID == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid respond args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ex, err := s.lookupExc(exceptionID)
	if err != nil {
		return err
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	if ex.view.Type != ETUnreachable {
		return &OpError{Code: ErrTypeMismatch, Msg: "not an unreachable exception"}
	}
	if err := s.checkActive(o, ex, exceptionID); err != nil {
		return err
	}
	s.commit(at)
	ex.view.Status = ESClosedUser
	o.view.ActiveException = ""
	o.view.Status = OSPicked
	return nil
}

// SubmitCorrection 在纠正窗口内提交新地址；右端点不允许，距离恰等允许。
func (s *System) SubmitCorrection(exceptionID string, x, y, at int64) error {
	if exceptionID == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid correction args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ex, err := s.lookupExc(exceptionID)
	if err != nil {
		return err
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	if ex.view.Type != ETWrongAddress {
		return &OpError{Code: ErrTypeMismatch, Msg: "not a wrong-address exception"}
	}
	if err := s.checkActive(o, ex, exceptionID); err != nil {
		return err
	}
	if at >= ex.view.Deadline {
		return &OpError{Code: ErrCorrectionWindow, Msg: "correction at/after window right edge"}
	}
	s.settleAt(o, at) // 窗口内提交：不存在需要提前的到期转化
	d := absInt64(x-ex.origX) + absInt64(y-ex.origY)
	if d > s.params.MaxCorrectionDist {
		return &OpError{Code: ErrDistanceExceeded,
			Msg: fmt.Sprintf("correction distance %d > %d", d, s.params.MaxCorrectionDist)}
	}
	s.commit(at)
	o.view.AddressX = x
	o.view.AddressY = y
	o.view.AddressChanges++
	ex.view.Status = ESClosedUser
	o.view.ActiveException = ""
	o.view.Status = OSPicked
	return nil
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
