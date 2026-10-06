package speq

// AddCategory 登记/替换类别配置。类别编码须非空，月数与各天数均须非负，
// 检验周期月数须为正。
func (s *System) AddCategory(cfg CategoryConfig) error {
	if cfg.Code == "" || cfg.PeriodMonths <= 0 ||
		cfg.EarlyWindowDays < 0 || cfg.MinUnsealDays < 0 || cfg.WarningLeadDays < 0 {
		return errf(ErrInvalidParameter, "类别配置非法: %+v", cfg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.categories[cfg.Code] = cfg
	return nil
}

// RegisterDevice 在 date 日登记一台设备，附带首次检验合格日期 firstPassDate，
// 到期日 = 首次检验合格日 + 一个检验周期。
func (s *System) RegisterDevice(date int, id, category string, firstPassDate int) (expiry int, err error) {
	return s.registerObject(date, id, category, firstPassDate, KindDevice)
}

// RegisterAttachment 在 date 日登记一个附件（安全阀或压力表）。
func (s *System) RegisterAttachment(date int, id, category string, kind ObjectKind, firstPassDate int) (expiry int, err error) {
	if kind != KindSafetyValve && kind != KindPressureGauge {
		return 0, errf(ErrInvalidParameter, "附件种类非法: %v", kind)
	}
	return s.registerObject(date, id, category, firstPassDate, kind)
}

func (s *System) registerObject(date int, id, category string, firstPassDate int, wantKind ObjectKind) (int, error) {
	if date < 0 || firstPassDate < 0 || !validID(id) || firstPassDate > date {
		return 0, errf(ErrInvalidParameter, "登记参数非法: date=%d id=%q firstPass=%d", date, id, firstPassDate)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if date < s.lastDate {
		return 0, errf(ErrDateRegression, "操作日期 %d 早于上一接受日期 %d", date, s.lastDate)
	}
	cfg, ok := s.categories[category]
	if !ok {
		return 0, errf(ErrNotFound, "类别不存在: %q", category)
	}
	if cfg.Kind != wantKind {
		return 0, errf(ErrIllegalState, "类别 %q 与登记种类不符", category)
	}
	if _, dup := s.objects[id]; dup {
		return 0, errf(ErrIllegalState, "编号已存在: %q", id)
	}

	expiry := AddCalendarMonths(firstPassDate, cfg.PeriodMonths)
	o := &obj{
		id:       id,
		category: category,
		kind:     wantKind,
		expiry:   expiry,
	}
	s.objects[id] = o
	s.indexAdd(o)
	s.lastDate = date
	return expiry, nil
}
