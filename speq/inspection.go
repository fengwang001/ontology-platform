package speq

// Inspect 在 date 日对对象实施检验。
//
//	合格           见合格基准规则；复检合格清除停用。
//	有条件合格     rectifyDays 为整改限期天数；新到期日取合格规则结果与
//	               检验日+整改限期的较早者；不清除既有的不合格停用。
//	不合格         立即停用，到期日不变。
//
// 封存状态下允许检验；检验后封存锚点更新为本次检验日期，
// 启封时只顺延该日期之后经过的封存天数。
func (s *System) Inspect(date int, id string, result InspectionResult, rectifyDays int) (newExpiry int, err error) {
	if date < 0 || !validID(id) {
		return 0, errf(ErrInvalidParameter, "检验参数非法: date=%d id=%q", date, id)
	}
	if result < ResultPass || result > ResultFail {
		return 0, errf(ErrInvalidParameter, "检验结果非法: %v", result)
	}
	if rectifyDays < 0 {
		return 0, errf(ErrInvalidParameter, "整改限期天数不能为负: %d", rectifyDays)
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

	cfg := s.categories[o.category]

	if result == ResultFail {
		// 到期日不变；立即停用并退出预警索引。
		if s.indexed(o) {
			s.indexRemove(o)
		}
		o.disabled = true
		if o.sealed {
			o.sealAnchor = date
		}
		s.lastDate = date
		return o.expiry, nil
	}

	// 合格基准规则：
	// 检验日在原到期日之前且相差不超过提前窗口（含边界、含到期日当天）时，
	// 以原到期日为基准加一个周期；否则（过早或已超期）以检验日为基准。
	basisExpiry := AddCalendarMonths(date, cfg.PeriodMonths)
	if date <= o.expiry && o.expiry-date <= cfg.EarlyWindowDays {
		basisExpiry = AddCalendarMonths(o.expiry, cfg.PeriodMonths)
	}
	newE := basisExpiry
	if result == ResultConditional {
		limit := date + rectifyDays
		if limit < newE {
			newE = limit
		}
	}

	wasIndexed := s.indexed(o)
	if wasIndexed {
		s.indexRemove(o)
	}
	o.expiry = newE
	if result == ResultPass {
		o.disabled = false
	}
	if o.sealed {
		// 封存期检验：锚点记为本次检验日期；封存中不进入索引。
		o.sealAnchor = date
	} else if s.indexed(o) {
		s.indexAdd(o)
	}
	s.lastDate = date
	return newE, nil
}
