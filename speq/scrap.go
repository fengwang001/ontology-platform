package speq

// Scrap 在 date 日报废对象，报废后不再接受任何操作。
// 已挂接附件的设备报废时附件自动脱离；附件报废时自动从设备摘除。
func (s *System) Scrap(date int, id string) error {
	if date < 0 || !validID(id) {
		return errf(ErrInvalidParameter, "报废参数非法: date=%d id=%q", date, id)
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

	if o.kind == KindDevice {
		// 设备报废：挂接附件全部自动脱离。
		for aid := range s.attachments[id] {
			if a, exists := s.objects[aid]; exists {
				a.host = ""
			}
		}
		delete(s.attachments, id)
	} else {
		s.detach(o)
	}

	if s.indexed(o) {
		s.indexRemove(o)
	}
	o.scrapped = true
	o.sealed = false
	o.disabled = false
	s.lastDate = date
	return nil
}
