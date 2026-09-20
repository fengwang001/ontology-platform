package ontology

import "fmt"

// Snapshot 是某次 Acquire 得到的配置快照。
// 快照与后续更新完全隔离：读到的永远是获取那一刻的内容。
// 快照的字段访问都由 Manager 的互斥锁保护，可并发使用。
type Snapshot struct {
	m        *Manager
	version  uint64
	released bool
}

// Acquire 取一份当前版本的快照，引用计数加一。
// 使用完毕后必须调用 Release 归还。
func (m *Manager) Acquire() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refs[m.current]++
	m.outstanding++
	return &Snapshot{m: m, version: m.current}
}

// Version 返回快照指向的版本号。
func (s *Snapshot) Version() uint64 { return s.version }

// lookup 在持锁状态下解析快照指向的版本。
// 版本已被回收时返回 ErrVersionReclaimed（优先于归还检查，
// 使"指向已回收版本的过期快照"得到可判定的错误）。
func (s *Snapshot) lookup() (*version, error) {
	v, ok := s.m.versions[s.version]
	if !ok {
		return nil, fmt.Errorf("version %d: %w", s.version, ErrVersionReclaimed)
	}
	if s.released {
		return nil, ErrSnapshotReleased
	}
	return v, nil
}

// Get 读取快照中字段 name 的值。
// 快照已归还时返回 ErrSnapshotReleased；
// 快照指向的版本已被回收时返回 ErrVersionReclaimed；
// 字段未声明时返回包含 ErrUnknownField 的 *ValidationError。
func (s *Snapshot) Get(name string) (any, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	v, err := s.lookup()
	if err != nil {
		return nil, err
	}
	val, ok := v.values[name]
	if !ok {
		return nil, &ValidationError{Field: name, Err: ErrUnknownField}
	}
	return val, nil
}

// SourceVersion 返回快照中字段 name 的来源版本号，
// 即该字段当前值是由哪一次更新写入的。错误语义同 Get。
func (s *Snapshot) SourceVersion(name string) (uint64, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	v, err := s.lookup()
	if err != nil {
		return 0, err
	}
	src, ok := v.sources[name]
	if !ok {
		return 0, &ValidationError{Field: name, Err: ErrUnknownField}
	}
	return src, nil
}

// Release 归还快照，引用计数减一。
// 幂等：重复调用是空操作，不会 panic，计数也不会减成负数。
func (s *Snapshot) Release() {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.released {
		return
	}
	s.released = true
	s.m.refs[s.version]--
	s.m.outstanding--
}
