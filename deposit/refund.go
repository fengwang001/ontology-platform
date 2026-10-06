package deposit

// liabilityFor 计算某笔应退款在 now 时点累计的违约金：
// 从起算日后第 C+1 天起，每日 rateNum/rateDen，算到 now 的前一日，向下取整。
// 恰在第 C 天当天（now = start+C）退还不计违约金；now <= start+C 时为 0。
func (s *Service) liabilityFor(t *tranche, now int) int64 {
	days := now - t.Start - s.cfg.C - 1
	if days <= 0 {
		return 0
	}
	return t.Amount * int64(days) * s.cfg.RateNum / s.cfg.RateDen
}

// Refund 房东一次性退清当前所有可退金额。
// 可退金额来自各未退 tranche（无争议部分、以及裁定释放部分）。
// 必须一次性退清，且可退金额为零时拒绝；部分退还不被支持，
// 配合互斥锁保证同一笔金额在并发下不会被退还两次。
// 返回本金（出自押金）与违约金（房东另行承担，不占用押金）。
func (s *Service) Refund(now int, leaseID string) (principal, liability int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.emit("Refund now=%d lease=%q -> REJECT %v", now, leaseID, err)
		} else {
			s.emit("Refund now=%d lease=%q -> OK principal=%d liability=%d", now, leaseID, principal, liability)
		}
	}()

	l, err := s.enter(now, leaseID, true)
	if err != nil {
		return 0, 0, err
	}
	s.settle(l, now)
	open := 0
	for _, t := range l.tranches {
		if !t.Refunded {
			open++
			principal += t.Amount
			liability += s.liabilityFor(t, now)
		}
	}
	if open == 0 || principal == 0 {
		return 0, 0, errf(ErrState, "no refundable amount")
	}
	for _, t := range l.tranches {
		if !t.Refunded {
			t.Refunded = true
			t.RefundAt = now
		}
	}
	l.refunded += principal
	s.now = now
	s.record(l, "refund", now, map[string]int64{"principal": principal, "liability": liability})
	return principal, liability, nil
}
