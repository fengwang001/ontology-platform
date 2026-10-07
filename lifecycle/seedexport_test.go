package lifecycle

// AddLinkForSeed 仅供对拍测试铺底初始链接（可能涉及终态实例的预存链接）。
func (s *Store) AddLinkForSeed(typ, from, to string) {
	s.lockIDs([]string{from})
	defer s.unlockIDs([]string{from})
	slot := s.inst[from]
	v := slot.load()
	nv := *v
	nv.out = cloneOut(v.out)
	if nv.out[typ] == nil {
		nv.out[typ] = map[string]bool{}
	}
	nv.out[typ][to] = true
	slot.store(&nv)
}
