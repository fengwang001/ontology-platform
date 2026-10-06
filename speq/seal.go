package speq

// Seal 在 date 日封存对象。封存要求对象当时未超期且未处于不合格停用。
// 封存期间暂停计时：对象退出到期预警索引。
func (s *System) Seal(date int, id string) error {
	if date < 0 || !validID(id) {
		return errf(ErrInvalidParameter, "封存参数非法: date=%d id=%q", date, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if date < s.lastDate {
		return errf(ErrDateRegression, "操作日期 %d 早于上一接受日期 %d", date, s.lastDate)
	}
	o, ok := s.objects[id]
	if !ok {
		return errf(ErrNotFound, "对象不存在: %q", id)
	}
	if o.scrapped {
		return errf(ErrScrapped, "对象已报废: %q", id)
	}
	if o.sealed {
		return errf(ErrIllegalState, "对象已处于封存状态: %q", id)
	}
	if o.disabled {
		return errf(ErrIllegalState, "对象处于不合格停用，不能封存: %q", id)
	}
	if date > o.expiry {
		return errf(ErrConditionNotMet, "对象已超期，不能封存: %q", id)
	}

	if s.indexed(o) {
		s.indexRemove(o)
	}
	o.sealed = true
	o.sealDate = date
	o.sealAnchor = date
	s.lastDate = date
	return nil
}

// Unseal 在 date 日启封对象。
//
// 普通启封：到期日顺延封存天数（启封日 - 封存日）。
// 封存期做过检验：到期日已由检验重算，本次启封只顺延「最后一次封存期检验
// 之后经过的封存天数」（启封日 - sealAnchor）。
//
// 顺延后若自启封日起剩余有效天数（expiry-date）少于启封最小保障天数，
// 拒绝启封（条件不满足），须在封存状态下先检验。
func (s *System) Unseal(date int, id string) (newExpiry int, err error) {
	if date < 0 || !validID(id) {
		return 0, errf(ErrInvalidParameter, "启封参数非法: date=%d id=%q", date, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if date < s.lastDate {
		return 0, errf(ErrDateRegression, "操作日期 %d 早于上一接受日期 %d", date, s.lastDate)
	}
	o, ok := s.objects[id]
	if !ok {
		return 0, errf(ErrNotFound, "对象不存在: %q", id)
	}
	if o.scrapped {
		return 0, errf(ErrScrapped, "对象已报废: %q", id)
	}
	if !o.sealed {
		return 0, errf(ErrIllegalState, "对象未处于封存状态: %q", id)
	}

	anchor := o.sealAnchor
	elapsed := date - anchor
	if elapsed < 0 {
		elapsed = 0
	}
	candidateExpiry := o.expiry + elapsed
	cfg := s.categories[o.category]
	if candidateExpiry-date < cfg.MinUnsealDays {
		return 0, errf(ErrConditionNotMet,
			"启封后剩余有效天数 %d 少于最小保障天数 %d，须先在封存状态下检验: %q",
			candidateExpiry-date, cfg.MinUnsealDays, id)
	}

	o.expiry = candidateExpiry
	o.sealed = false
	o.sealDate = 0
	o.sealAnchor = 0
	if s.indexed(o) {
		s.indexAdd(o)
	}
	s.lastDate = date
	return o.expiry, nil
}
