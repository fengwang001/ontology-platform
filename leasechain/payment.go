package leasechain

// PayArrear 由欠费租约自身或其任一上级（连带责任）清偿欠费 amount。
// 清偿后其下各级同一笔欠费视为已清偿；每笔清偿恰好形成一笔等额追偿权。
func (s *Service) PayArrear(now int, arrearID int64, payerLeaseID int64, amount int64) (*Recourse, error) {
	if arrearID <= 0 || payerLeaseID <= 0 || amount <= 0 {
		return nil, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out *Recourse
	err := s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		a := tx.arrears[arrearID]
		payer := tx.leases[payerLeaseID]
		if a == nil || payer == nil {
			return ErrLeaseUnavailable
		}
		debtor := tx.leases[a.LeaseID]
		if debtor == nil {
			return ErrLeaseUnavailable
		}
		responsible := false
		for _, anc := range tx.ancestorsLocked(debtor) {
			if anc.ID == payerLeaseID {
				responsible = true
				break
			}
		}
		if !responsible {
			return wrapErr(ErrIllegalState, "该租约对此欠费无连带责任")
		}
		if a.Paid >= a.Amount {
			return wrapErr(ErrIllegalState, "欠费已清偿再清偿")
		}
		if a.Paid+amount > a.Amount {
			return wrapErr(ErrIllegalState, "清偿额超过欠费余额")
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		a = tx.arrears[arrearID]
		if a.Paid >= a.Amount {
			return wrapErr(ErrIllegalState, "欠费已清偿再清偿")
		}
		if a.Paid+amount > a.Amount {
			return wrapErr(ErrIllegalState, "清偿额超过欠费余额")
		}
		a.Paid += amount
		tx.nextID++
		r := &Recourse{
			ID:       tx.nextID,
			ArrearID: arrearID,
			PayerID:  payerLeaseID,
			Amount:   amount,
		}
		tx.recourses[r.ID] = r
		out = &Recourse{ID: r.ID, ArrearID: r.ArrearID, PayerID: r.PayerID, Amount: r.Amount}
		return nil
	})
	return out, err
}

// ResponsibleChain 返回欠费当前责任人链（自欠费租约起逐级向上），
// 仅沿父指针行走，步数只与该欠费所在链深度有关、与系统内租约总数无关。
func (s *Service) ResponsibleChain(arrearID int64) ([]int64, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.arrears[arrearID]
	if a == nil {
		return nil, 0, ErrLeaseUnavailable
	}
	debtor := s.leases[a.LeaseID]
	if debtor == nil {
		return nil, 0, ErrLeaseUnavailable
	}
	steps := 0
	var ids []int64
	cur := debtor
	for {
		steps++
		ids = append(ids, cur.ID)
		if cur.ParentID == 0 {
			break
		}
		cur = s.leases[cur.ParentID]
		steps++
	}
	return ids, steps, nil
}

// RecourseParties 返回一笔欠费已形成追偿权的（追偿方 -> 欠费租约）清单。
func (s *Service) RecourseParties(arrearID int64) []Recourse {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Recourse
	for _, r := range s.recourses {
		if r.ArrearID == arrearID {
			out = append(out, *r)
		}
	}
	return out
}
