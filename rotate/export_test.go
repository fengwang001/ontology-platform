package rotate

import "ontology/cert"

// seedActive 直接安放一张 Active 证书（仅供白盒测试）。
func (s *Service) seedActive(serial, dev string, nb, na int64) {
	s.bySerial[serial] = &rec{cert: cert.Cert{Serial: serial, Dev: dev, Nb: nb, Na: na}, st: cert.Active}
	s.getSlot(dev).active = serial
}

// seedRetiring 直接安放一张 Retiring 证书并登记到期条目（仅供白盒测试）。
func (s *Service) seedRetiring(serial, dev string, nb, na, retireAt int64) {
	r := &rec{cert: cert.Cert{Serial: serial, Dev: dev, Nb: nb, Na: na}, st: cert.Retiring, retireAt: retireAt}
	s.pushEntry(r, kindRetire, retireAt)
	s.bySerial[serial] = r
	s.getSlot(dev).retiring = serial
}

// heapLen 返回到期堆大小（仅供白盒测试）。
func (s *Service) heapLen() int { return len(s.entries) }

// devState 返回某设备三张槽位（仅供白盒测试）。
func (s *Service) devState(dev string) (active, pending, retiring string) {
	sl := s.devs[dev]
	if sl == nil {
		return "", "", ""
	}
	return sl.active, sl.pending, sl.retiring
}

// recordState 返回证书状态与到期字段（仅供白盒测试）。
func (s *Service) recordState(serial string) (st cert.State, lapseAt, retireAt int64, ok bool) {
	r := s.bySerial[serial]
	if r == nil {
		return 0, 0, 0, false
	}
	return r.st, r.lapseAt, r.retireAt, true
}

// setLastNow 直接设置逻辑时钟（仅供白盒测试）。
func (s *Service) setLastNow(now int64) { s.lastNow = now }
