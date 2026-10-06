package deposit

// NewService 创建押金服务。logger 可为 nil；非 nil 时每步输入/输出/判定依据会写入日志。
func NewService(cfg Config, logger func(string)) *Service {
	return newService(cfg, logger)
}

// Checkout 登记一份租约的退房日与押金总额。
func (s *Service) Checkout(now int, leaseID string, deposit int64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.emit("Checkout now=%d lease=%q deposit=%d -> REJECT %v", now, leaseID, deposit, err)
		} else {
			s.emit("Checkout now=%d lease=%q deposit=%d -> OK", now, leaseID, deposit)
		}
	}()

	if leaseID == "" {
		return errf(ErrIllegalArgument, "empty lease id")
	}
	if now < s.now {
		return errf(ErrClockRollback, "now=%d < last=%d", now, s.now)
	}
	if deposit <= 0 || deposit > maxAmount {
		return errf(ErrAmount, "deposit %d out of (0,%d]", deposit, maxAmount)
	}
	if _, exists := s.leases[leaseID]; exists {
		return errf(ErrState, "lease %q already exists", leaseID)
	}
	s.leases[leaseID] = &lease{
		id: leaseID, deposit: deposit, checkout: now,
		claims: map[int]*Claim{},
	}
	s.now = now
	return nil
}

// FileClaim 房东在申报期内申报一条扣项，返回扣项 ID。
func (s *Service) FileClaim(now int, leaseID string, cat Category, amount int64) (id int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.emit("FileClaim now=%d lease=%q cat=%d amount=%d -> REJECT %v", now, leaseID, cat, amount, err)
		} else {
			s.emit("FileClaim now=%d lease=%q cat=%d amount=%d -> OK id=%d", now, leaseID, cat, amount, id)
		}
	}()

	l, err := s.enter(now, leaseID, true)
	if err != nil {
		return 0, err
	}
	if !cat.valid() {
		return 0, errf(ErrIllegalArgument, "bad category %d", cat)
	}
	// 申报期截止日 = checkout + A；逾期（now 更大）一律拒绝。
	if now > l.checkout+s.cfg.A {
		return 0, errf(ErrLate, "file at day %d past filing deadline %d", now, l.checkout+s.cfg.A)
	}
	if amount <= 0 || amount > maxAmount {
		return 0, errf(ErrAmount, "claim amount %d out of (0,%d]", amount, maxAmount)
	}
	c := &Claim{
		ID: l.nextID, Category: cat, Amount: amount, FiledAt: now, Seq: len(l.order),
	}
	l.nextID++
	l.claims[c.ID] = c
	l.order = append(l.order, c)
	s.now = now
	s.record(l, "file", now, map[string]int64{"id": int64(c.ID), "cat": int64(cat), "amount": amount})
	return c.ID, nil
}

// WithdrawClaim 在申报期内撤销一条扣项。
func (s *Service) WithdrawClaim(now int, leaseID string, claimID int) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.emit("Withdraw now=%d lease=%q claim=%d -> REJECT %v", now, leaseID, claimID, err)
		} else {
			s.emit("Withdraw now=%d lease=%q claim=%d -> OK", now, leaseID, claimID)
		}
	}()

	l, err := s.enter(now, leaseID, true)
	if err != nil {
		return err
	}
	if now > l.checkout+s.cfg.A {
		return errf(ErrLate, "withdraw at day %d past filing deadline %d", now, l.checkout+s.cfg.A)
	}
	c, ok := l.claims[claimID]
	if !ok {
		return errf(ErrState, "claim %d not found", claimID)
	}
	if c.Status == csWithdrawn {
		return errf(ErrState, "claim %d already withdrawn", claimID)
	}
	c.Status = csWithdrawn
	// 从受偿次序中剔除；金额不可修改，撤销只影响截止时的受偿计算。
	for i, x := range l.order {
		if x == c {
			l.order = append(l.order[:i], l.order[i+1:]...)
			break
		}
	}
	s.now = now
	s.record(l, "withdraw", now, map[string]int64{"id": int64(claimID)})
	return nil
}

// Dispute 租户在争议期内对一条扣项提出争议。
func (s *Service) Dispute(now int, leaseID string, claimID int) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.emit("Dispute now=%d lease=%q claim=%d -> REJECT %v", now, leaseID, claimID, err)
		} else {
			s.emit("Dispute now=%d lease=%q claim=%d -> OK (frozen)", now, leaseID, claimID)
		}
	}()

	l, err := s.enter(now, leaseID, true)
	if err != nil {
		return err
	}
	// 申报期必须已结束：争议只能在申报期结束后提出。
	deadline := l.checkout + s.cfg.A
	if now <= deadline {
		return errf(ErrState, "filing period not closed at day %d", now)
	}
	s.settle(l, now)
	end := deadline + s.cfg.B
	if now > end {
		return errf(ErrLate, "dispute at day %d past dispute deadline %d", now, end)
	}
	c, ok := l.claims[claimID]
	if !ok || c.Status == csWithdrawn {
		return errf(ErrState, "claim %d not disputable", claimID)
	}
	if c.Status == csDisputed || c.Status == csAdjudged {
		return errf(ErrState, "claim %d already disputed", claimID)
	}
	c.Status = csDisputed
	s.now = now
	s.record(l, "dispute", now, map[string]int64{"id": int64(claimID), "frozen": c.Paid})
	return nil
}

// Adjudicate 对一条争议中的扣项作出裁定。
func (s *Service) Adjudicate(now int, leaseID string, claimID int, amount int64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.emit("Adjudicate now=%d lease=%q claim=%d amount=%d -> REJECT %v", now, leaseID, claimID, amount, err)
		} else {
			s.emit("Adjudicate now=%d lease=%q claim=%d amount=%d -> OK", now, leaseID, claimID, amount)
		}
	}()

	l, err := s.enter(now, leaseID, true)
	if err != nil {
		return err
	}
	s.settle(l, now)
	c, ok := l.claims[claimID]
	if !ok || c.Status == csWithdrawn {
		return errf(ErrState, "claim %d not adjudicable", claimID)
	}
	if c.Status == csAdjudged {
		return errf(ErrState, "claim %d already adjudged", claimID)
	}
	if c.Status != csDisputed {
		return errf(ErrState, "claim %d not in dispute", claimID)
	}
	if amount < 0 || amount > c.Amount {
		return errf(ErrAmount, "adjudged amount %d out of [0,%d]", amount, c.Amount)
	}

	// O(1) 重算：只用该扣项自身的 Paid（冻结金额）。
	frozen := c.Paid
	release := frozen - min64(frozen, amount) // 释放回租户的押金
	vest := frozen - release                  // 裁定支持且在冻结额度内的部分
	c.Vested = vest
	c.AdjudgedAmount = amount
	c.Status = csAdjudged
	if release > 0 {
		// 裁定释放部分从裁定日重新起算 C 天时限。
		l.tranches = append(l.tranches, &tranche{Amount: release, Start: now})
	}
	s.now = now
	s.record(l, "adjudge", now, map[string]int64{
		"id": int64(claimID), "amount": amount, "release": release, "vest": vest,
	})
	return nil
}

// View 返回某租约当前的对账快照。

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
