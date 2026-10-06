package loto

// RegisterPerson 登记人员及其角色（可多次调用以追加角色）。
// 配置操作不占用逻辑时钟。
func (s *System) RegisterPerson(id string, roles ...Role) error {
	if id == "" {
		return fail(InvalidParam, "person id is empty")
	}
	valid := map[Role]bool{
		RoleApplicant: true, RoleApprover: true, RoleWorker: true, RoleSupervisor: true,
	}
	rs := map[Role]bool{}
	for _, r := range roles {
		if !valid[r] {
			return fail(InvalidParam, "unknown role %q", r)
		}
		rs[r] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.st.persons[id]
	if !ok {
		p = &Person{ID: id, Roles: map[Role]bool{}}
		s.st.persons[id] = p
	}
	for r := range rs {
		p.Roles[r] = true
	}
	return nil
}

// RegisterDevice 登记设备及其依赖的隔离点集合（幂等合并，重复点号去重）。
func (s *System) RegisterDevice(id string, points []string) error {
	if id == "" {
		return fail(InvalidParam, "device id is empty")
	}
	pset := map[string]bool{}
	for _, pt := range points {
		if pt == "" {
			return fail(InvalidParam, "device %q has an empty point id", id)
		}
		pset[pt] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.st.devices[id]
	if !ok {
		existing = map[string]bool{}
		s.st.devices[id] = existing
	}
	for pt := range pset {
		existing[pt] = true
	}
	return nil
}

// AuditLog 返回审计日志的副本（只读 API）。
func (s *System) AuditLog() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEntry, len(s.st.audit))
	copy(out, s.st.audit)
	return out
}

// Clock 返回最近一次被接受操作的时刻。
func (s *System) Clock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.clock
}

// ActivePermitCount 返回当前占用态票数（供规模对照测试）。
func (s *System) ActivePermitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.st.permits {
		if p.occupied() {
			n++
		}
	}
	return n
}
