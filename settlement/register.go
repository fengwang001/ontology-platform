package settlement

// register.go —— 指令登记、参数校验与只读查询。

func validOrder(o Order) bool {
	if o.ID <= 0 || o.Security <= 0 || o.Buyer == "" || o.Seller == "" ||
		o.Buyer == o.Seller || o.Qty <= 0 || o.Price <= 0 || o.SettleDay <= 0 {
		return false
	}
	return true
}

// RegisterOrder 登记成交指令。
// 错误优先级：参数非法 > 编号重复 > 账户不存在 > 应交割日已过。
func (s *System) RegisterOrder(o Order) error {
	if !validOrder(o) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.orders[o.ID]; dup {
		return ErrDuplicateID
	}
	_, bok := s.accounts[o.Buyer]
	_, sok := s.accounts[o.Seller]
	if !bok || !sok {
		return ErrAccountMissing
	}
	if _, isBiz := s.bizDays[o.SettleDay]; !isBiz {
		return ErrInvalidParam
	}
	if s.lastBatchDay != 0 && o.SettleDay < s.lastBatchDay {
		return ErrDatePassed
	}
	ins := &instruction{Order: o, status: StatusPending}
	s.orders[o.ID] = ins
	if s.lastBatchDay != 0 && o.SettleDay <= s.lastBatchDay {
		s.insertOverdue(ins)
	} else {
		s.insertByDay(ins)
	}
	return nil
}

// insertOverdue 按编号升序插入滚动集合。
func (s *System) insertOverdue(ins *instruction) {
	bucket := s.overdue
	idx := len(bucket)
	for i, e := range bucket {
		if ins.ID < e.ID {
			idx = i
			break
		}
	}
	bucket = append(bucket, nil)
	copy(bucket[idx+1:], bucket[idx:])
	bucket[idx] = ins
	s.overdue = bucket
}

// insertByDay 按编号升序把指令插入对应应交割日桶。
// insertByDay 按编号升序把指令插入对应应交割日桶。
func (s *System) insertByDay(ins *instruction) {
	bucket := s.byDay[ins.SettleDay]
	idx := len(bucket)
	for i, e := range bucket {
		if ins.ID < e.ID {
			idx = i
			break
		}
	}
	bucket = append(bucket, nil)
	copy(bucket[idx+1:], bucket[idx:])
	bucket[idx] = ins
	s.byDay[ins.SettleDay] = bucket
}
