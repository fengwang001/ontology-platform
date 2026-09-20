package ontology

// Snapshot 是某一版本配置的只读引用，必须在用完后调用 Release 归还。
type Snapshot struct {
	id      int64
	version int64
	mgr     *Manager
}

// Version 返回快照固定指向的版本号（不随后续更新变化）。
func (s *Snapshot) Version() int64 { return s.version }

// ID 返回快照实例的唯一编号。
func (s *Snapshot) ID() int64 { return s.id }

// Get 读取快照中某个字段的值。
// 快照已归还返回 ErrReleased；指向的版本已被回收返回 ErrVersionReclaimed；
// 字段未声明返回 ErrFieldNotFound。
func (s *Snapshot) Get(field string) (Value, error) {
	s.mgr.mu.Lock()
	defer s.mgr.mu.Unlock()
	if _, active := s.mgr.active[s]; !active {
		return Value{}, ErrReleased
	}
	vd, ok := s.mgr.versions[s.version]
	if !ok {
		return Value{}, ErrVersionReclaimed
	}
	v, ok := vd.values[field]
	if !ok {
		return Value{}, ErrFieldNotFound
	}
	return v, nil
}

// FieldVersion 返回某个字段当前值的来源版本（字段级版本）。
func (s *Snapshot) FieldVersion(field string) (int64, error) {
	s.mgr.mu.Lock()
	defer s.mgr.mu.Unlock()
	if _, active := s.mgr.active[s]; !active {
		return 0, ErrReleased
	}
	vd, ok := s.mgr.versions[s.version]
	if !ok {
		return 0, ErrVersionReclaimed
	}
	src, ok := vd.sources[field]
	if !ok {
		return 0, ErrFieldNotFound
	}
	return src, nil
}

// Release 归还快照。首次归还返回 true；重复归还幂等，返回 false，
// 不会 panic，也不会把引用计数减成负数。
func (s *Snapshot) Release() bool { return s.mgr.release(s) }
