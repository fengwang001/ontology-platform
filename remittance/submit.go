package remittance

func (e *Engine) validateSubmitRequest(req SubmitRequest) (*account, error) {
	switch {
	case req.Sender == "":
		return nil, newError(ErrCodeInvalidArgument, "submit: empty sender")
	case req.Payee == "":
		return nil, newError(ErrCodeInvalidArgument, "submit: empty payee")
	case req.IdemKey == "":
		return nil, newError(ErrCodeInvalidArgument, "submit: empty idempotency key")
	case req.QuoteID <= 0:
		return nil, newError(ErrCodeInvalidArgument, "submit: quote id must be positive")
	case req.Now < 0:
		return nil, newError(ErrCodeInvalidArgument, "submit: now must be non-negative")
	case e.cfg.ReviewSeconds <= 0:
		return nil, newError(ErrCodeInvalidArgument, "submit: review window must be positive")
	}
	a, ok := e.accounts[req.Sender]
	if !ok {
		return nil, newError(ErrCodeInvalidArgument, "submit: unknown sender %q", req.Sender)
	}
	return a, nil
}

// sameSubmitParams 判断幂等重放的参数是否一致（时间戳不参与比较：
// 重放天然携带更晚的 now）。
func sameSubmitParams(a, b SubmitRequest) bool {
	return a.Sender == b.Sender && a.QuoteID == b.QuoteID &&
		a.Payee == b.Payee && a.IdemKey == b.IdemKey
}

// Submit 提交一笔汇款。
//
// 错误只按以下优先级报第一个：
// 参数非法 > 时钟回退 > 收款人命中制裁 > 幂等冲突 >
// 报价不存在或已消耗 > 报价已过期 > 单笔 > 日 > 年度。
//
// 幂等键与参数完全一致的重放在制裁检查之后直接返回原结果。
// 被拒绝的提交：不物化逾期、不消耗报价、不记幂等键、不推进时钟。
func (e *Engine) Submit(req SubmitRequest) (SubmitResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. 参数非法
	acc, err := e.validateSubmitRequest(req)
	if err != nil {
		return SubmitResult{}, err
	}
	// 2. 时钟回退
	if err := e.checkClock(req.Now); err != nil {
		return SubmitResult{}, err
	}
	// 3. 收款人命中制裁（幂等重放也要先过制裁：名单是外部事实）
	if _, bad := e.sanctioned[req.Payee]; bad {
		return SubmitResult{}, newError(ErrCodeSanctionedPayee,
			"submit: payee %q is sanctioned", req.Payee)
	}
	// 4. 幂等
	if rec, ok := acc.idem[req.IdemKey]; ok {
		if !sameSubmitParams(rec.req, req) {
			return SubmitResult{}, newError(ErrCodeIdempotencyConflict,
				"submit: idempotency key %q reused with different parameters", req.IdemKey)
		}
		// 完全一致的重放：原样返回首次结果。重放本身是被接受的操作，
		// 先把逾期事实物化，再推进时钟；不重复占用、不消耗报价。
		e.materializeSender(acc, req.Now)
		acc.advance(dayIndex(req.Now))
		e.lastNow = req.Now
		out := rec.result
		out.Replay = true
		return out, nil
	}
	// 5. 报价不存在或已消耗
	q := e.quotes[req.QuoteID]
	if q == nil || q.consumed {
		return SubmitResult{}, newError(ErrCodeQuoteNotFound,
			"submit: quote %d not found or already consumed", req.QuoteID)
	}
	if q.sender != req.Sender {
		// 他人的报价对本汇款人等同不存在，避免泄露报价归属。
		return SubmitResult{}, newError(ErrCodeQuoteNotFound,
			"submit: quote %d not found or already consumed", req.QuoteID)
	}
	// 6. 报价已过期（到期时刻恰等仍有效）
	if req.Now > q.expiresAt {
		return SubmitResult{}, newError(ErrCodeQuoteExpired,
			"submit: quote %d expired at %d (now=%d)", q.id, q.expiresAt, req.Now)
	}

	// 金额换算：128 位中间积。目标额向下取整（入账），占用额向上取整。
	target, ok1 := mulDivFloor(q.amount, q.ratePPM, rateScale)
	occ, ok2 := mulDivCeil(q.amount, q.ratePPM, rateScale)
	if !ok1 || !ok2 {
		return SubmitResult{}, newError(ErrCodeInvalidArgument,
			"submit: amount*rate overflows int64")
	}

	day := dayIndex(req.Now)
	// 限额校验使用“物化 + 推进到 now 后”的纯函数投影，
	// 保证即便因限额被拒，此刻也没有任何状态被改变。
	dayUsed, annualUsed := acc.projectUsage(req.Now, occ)
	// 7. 单笔限额（恰等视为满足）
	if occ > acc.lim.Single {
		return SubmitResult{}, newError(ErrCodeSingleLimitExceeded,
			"submit: occupied %d exceeds single limit %d", occ, acc.lim.Single)
	}
	// 8. 日限额
	if dayUsed > acc.lim.Daily {
		return SubmitResult{}, newError(ErrCodeDailyLimitExceeded,
			"submit: day %d used %d exceeds daily limit %d", day, dayUsed, acc.lim.Daily)
	}
	// 9. 年度额度
	if annualUsed > acc.lim.Annual {
		return SubmitResult{}, newError(ErrCodeAnnualLimitExceeded,
			"submit: rolling-year used %d exceeds annual limit %d", annualUsed, acc.lim.Annual)
	}

	// ---- 全部校验通过：以下为提交的唯一“改变状态”区域 ----
	e.materializeSender(acc, req.Now)
	acc.advance(day)

	e.nextTransferID++
	t := &transfer{
		id:           e.nextTransferID,
		sender:       req.Sender,
		payee:        req.Payee,
		quoteID:      q.id,
		sourceAmount: q.amount,
		ratePPM:      q.ratePPM,
		targetAmount: target,
		occupied:     occ,
		day:          day,
		submittedAt:  req.Now,
		idemKey:      req.IdemKey,
	}

	var deadline int64
	if target >= e.cfg.ReviewThreshold {
		t.status = StatusPending
		deadline = req.Now + e.cfg.ReviewSeconds
		t.reviewDeadline = deadline
		acc.addPending(t)
	} else {
		t.status = StatusSucceeded
		t.decidedAt = req.Now
	}

	acc.reserve(day, occ)
	q.consumed = true
	e.transfers[t.id] = t

	res := SubmitResult{
		TransferID:     t.id,
		TargetAmount:   target,
		Occupied:       occ,
		Status:         t.status,
		ReviewDeadline: deadline,
	}
	acc.idem[req.IdemKey] = idemRecord{req: req, result: res}
	e.lastNow = req.Now
	return res, nil
}
