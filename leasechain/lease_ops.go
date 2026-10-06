package leasechain

// GrantGeneral 授予概括性同意（由房东对某承租人）。
func (s *Service) GrantGeneral(now int, landlord, tenant string) error {
	if landlord == "" || tenant == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		tx.clock++
		tx.grantGeneral(landlord, tenant, tx.clock)
		return nil
	})
}

// RevokeGeneral 撤回概括性同意；重复撤回幂等、不留额外痕迹。
func (s *Service) RevokeGeneral(now int, landlord, tenant string) error {
	if landlord == "" || tenant == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		tx.clock++
		tx.revokeGeneral(landlord, tenant, tx.clock)
		return nil
	})
}

// GrantOneTime 授予一次性同意（绑定确定的拟转租条款）。
func (s *Service) GrantOneTime(now int, landlord, tenant string, parentID int64, start, end int, rent int64) (int64, error) {
	if landlord == "" || tenant == "" || parentID <= 0 || start >= end || rent <= 0 {
		return 0, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var id int64
	err := s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		p := tx.leases[parentID]
		if p == nil || p.Terminated {
			return ErrLeaseUnavailable
		}
		if landlord != p.LandlordID {
			return wrapErr(ErrIllegalState, "仅该链房东可授予同意")
		}
		tx.nextID++
		c := &OneTimeConsent{
			ID:         tx.nextID,
			LandlordID: landlord,
			ParentID:   parentID,
			TenantID:   tenant,
			Start:      start,
			End:        end,
			Rent:       rent,
			GrantAt:    tx.clock,
		}
		tx.oneTimes = append(tx.oneTimes, c)
		id = c.ID
		return nil
	})
	return id, err
}

// Sublease 发起一次转租。错误严格按固定次序判定，任何拒绝都不留痕。
func (s *Service) Sublease(now int, parentID int64, tenant string, start, end int, rent int64) (*Lease, error) {
	if parentID <= 0 || tenant == "" || start >= end || rent <= 0 {
		return nil, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out *Lease
	err := s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		parent := tx.leases[parentID]
		if parent == nil || parent.Terminated {
			return ErrLeaseUnavailable
		}
		if tenant == parent.TenantID {
			return wrapErr(ErrIllegalState, "对自身转租")
		}
		if tx.findOneTime(parent.LandlordID, tenant, parentID, start, end, rent) == nil &&
			!tx.generalValidAt(parent.LandlordID, tenant, tx.clock) {
			return ErrNoConsent
		}
		if start < parent.Start || end > parent.End {
			return ErrTermOutOfRange
		}
		if rent*100 > parent.Rent*int64(tx.cfg.P) {
			return ErrRentTooHigh
		}
		if tx.depthLocked(parent)+1 > tx.cfg.D {
			return ErrDepthExceeded
		}
		if tx.childLocked(parentID) != nil {
			return wrapErr(ErrIllegalState, "已有有效下级")
		}

		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		// 结算可能因到期级联改变 parent，需重新读取并复核关键状态。
		parent = tx.leases[parentID]
		if parent == nil || parent.Terminated {
			return ErrLeaseUnavailable
		}
		if tx.childLocked(parentID) != nil {
			return wrapErr(ErrIllegalState, "已有有效下级")
		}
		tx.nextID++
		l := &Lease{
			ID:         tx.nextID,
			LandlordID: parent.LandlordID,
			TenantID:   tenant,
			ParentID:   parentID,
			Start:      start,
			End:        end,
			Rent:       rent,
		}
		if c := tx.findOneTime(parent.LandlordID, tenant, parentID, start, end, rent); c != nil {
			c.Used = true
		}
		tx.leases[l.ID] = l
		parent.ChildID = l.ID
		out = cloneLease(l)
		return nil
	})
	return out, err
}

// Recognize 由房东对当前有效且尚未承认的租约授予独立承认。
func (s *Service) Recognize(now int, leaseID int64, landlord string) error {
	if leaseID <= 0 || landlord == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		l := tx.leases[leaseID]
		if l == nil || l.Terminated {
			return ErrLeaseUnavailable
		}
		if l.LandlordID != landlord {
			return wrapErr(ErrIllegalState, "仅房东可承认")
		}
		if l.ParentID == 0 {
			return wrapErr(ErrIllegalState, "直接租约无需承认")
		}
		if l.Recognized {
			return wrapErr(ErrIllegalState, "重复承认")
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		l = tx.leases[leaseID]
		if l == nil || l.Terminated {
			return ErrLeaseUnavailable
		}
		if l.Recognized || l.ParentID == 0 {
			return wrapErr(ErrIllegalState, "承认状态冲突")
		}
		l.Recognized = true
		return nil
	})
}
