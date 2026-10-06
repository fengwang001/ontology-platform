package speq

// MountAttachment 在 date 日把附件 attachmentID 挂接到设备 deviceID，
// 或从原设备转移到新设备。
//
// 转移要求附件当前未超期、未封存、未停用；附件已在该设备上时幂等成功。
func (s *System) MountAttachment(date int, attachmentID, deviceID string) error {
	if date < 0 || !validID(attachmentID) || !validID(deviceID) {
		return errf(ErrInvalidParameter, "挂接参数非法: date=%d attachment=%q device=%q",
			date, attachmentID, deviceID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if date < s.lastDate {
		return errf(ErrDateRegression, "操作日期 %d 早于上一接受日期 %d", date, s.lastDate)
	}
	a, ok := s.objects[attachmentID]
	if !ok {
		return errf(ErrNotFound, "附件不存在: %q", attachmentID)
	}
	if a.scrapped {
		return errf(ErrScrapped, "附件已报废: %q", attachmentID)
	}
	if a.kind != KindSafetyValve && a.kind != KindPressureGauge {
		return errf(ErrIllegalState, "对象不是附件: %q", attachmentID)
	}

	d, ok := s.objects[deviceID]
	if !ok {
		return errf(ErrNotFound, "设备不存在: %q", deviceID)
	}
	if d.scrapped {
		return errf(ErrScrapped, "设备已报废: %q", deviceID)
	}
	if d.kind != KindDevice {
		return errf(ErrIllegalState, "对象不是设备: %q", deviceID)
	}

	// 转移资格：未超期、未封存、未停用。
	if a.sealed {
		return errf(ErrIllegalState, "附件封存中，不能挂接/转移: %q", attachmentID)
	}
	if a.disabled {
		return errf(ErrConditionNotMet, "附件处于不合格停用，不能挂接/转移: %q", attachmentID)
	}
	if date > a.expiry {
		return errf(ErrConditionNotMet, "附件已超期，不能挂接/转移: %q", attachmentID)
	}

	if a.host == deviceID {
		s.lastDate = date
		return nil
	}
	if a.host != "" {
		delete(s.attachments[a.host], attachmentID)
	}
	a.host = deviceID
	if s.attachments[deviceID] == nil {
		s.attachments[deviceID] = make(map[string]struct{})
	}
	s.attachments[deviceID][attachmentID] = struct{}{}
	s.lastDate = date
	return nil
}

// detach 把附件从其设备摘除（报废时内部使用），调用方须持锁。
func (s *System) detach(a *obj) {
	if a.host == "" {
		return
	}
	if set, ok := s.attachments[a.host]; ok {
		delete(set, a.id)
	}
	a.host = ""
}
