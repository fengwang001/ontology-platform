package speq

// indexed 报告对象当前是否应进入到期预警索引：
// 未报废、未封存、未停用。
func (s *System) indexed(o *obj) bool {
	return !o.scrapped && !o.sealed && !o.disabled
}

// indexAdd / indexRemove 封装索引维护。
func (s *System) indexAdd(o *obj) {
	s.expiryIndex.insert(o.expiry, o.id)
}

func (s *System) indexRemove(o *obj) {
	s.expiryIndex.delete(o.expiry, o.id)
}

// setExpiry 修改到期日并同步索引（若对象在索引中）。
func (s *System) setExpiry(o *obj, newExpiry int) {
	inIndex := s.indexed(o)
	if inIndex {
		s.indexRemove(o)
	}
	o.expiry = newExpiry
	if inIndex {
		s.indexAdd(o)
	}
}

// validID 编号只允许非空。
func validID(id string) bool {
	return id != ""
}
