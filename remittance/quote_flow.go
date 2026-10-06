package remittance

func (e *Engine) validateQuoteRequest(req QuoteRequest) error {
	switch {
	case req.Sender == "":
		return newError(ErrCodeInvalidArgument, "quote: empty sender")
	case req.SourceCCY == "" || req.TargetCCY == "":
		return newError(ErrCodeInvalidArgument, "quote: empty currency")
	case req.SourceCCY == req.TargetCCY:
		return newError(ErrCodeInvalidArgument, "quote: source and target currency must differ")
	case req.Amount <= 0:
		return newError(ErrCodeInvalidArgument, "quote: amount must be positive, got %d", req.Amount)
	case req.RatePPM <= 0:
		return newError(ErrCodeInvalidArgument, "quote: rate ppm must be positive, got %d", req.RatePPM)
	case req.Now < 0:
		return newError(ErrCodeInvalidArgument, "quote: now must be non-negative, got %d", req.Now)
	case e.cfg.QuoteTTLSeconds <= 0:
		return newError(ErrCodeInvalidArgument, "quote: system TTL must be positive")
	}
	if _, ok := e.accounts[req.Sender]; !ok {
		return newError(ErrCodeInvalidArgument, "quote: unknown sender %q", req.Sender)
	}
	// 提前用与提交相同的 128 位运算校验可表示性，避免报价注定无法提交。
	if _, ok := mulDivCeil(req.Amount, req.RatePPM, rateScale); !ok {
		return newError(ErrCodeInvalidArgument, "quote: amount*rate overflows int64")
	}
	return nil
}

// ApplyQuote 申请锁汇报价。
//
// 校验优先级：参数非法 > 时钟回退。被拒绝的申请不分配编号、不推进时钟。
func (e *Engine) ApplyQuote(req QuoteRequest) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.validateQuoteRequest(req); err != nil {
		return 0, err
	}
	if err := e.checkClock(req.Now); err != nil {
		return 0, err
	}

	e.nextQuoteID++
	q := &quote{
		id:        e.nextQuoteID,
		sender:    req.Sender,
		sourceCCY: req.SourceCCY,
		targetCCY: req.TargetCCY,
		amount:    req.Amount,
		ratePPM:   req.RatePPM,
		createdAt: req.Now,
		expiresAt: req.Now + e.cfg.QuoteTTLSeconds,
	}
	e.quotes[q.id] = q
	e.lastNow = req.Now
	return q.id, nil
}
