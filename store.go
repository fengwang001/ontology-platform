package ontology

// store 保存提交与标签；非并发安全，由 Service 统一加锁。
// 两份 map 提供 O(1) 按标识查询，链路遍历只沿第一父指针，
// 因此裁决开销与分支之外的提交总数无关。
type store struct {
	commits map[string]*Commit
	tags    map[string]*Tag
}

func newStore() *store {
	return &store{commits: map[string]*Commit{}, tags: map[string]*Tag{}}
}

func (s *store) getCommit(id string) (*Commit, bool) {
	c, ok := s.commits[id]
	return c, ok
}

func (s *store) addCommit(c Commit) error {
	if _, dup := s.commits[c.ID]; dup {
		return ErrInvalidArgument
	}
	// 允许父提交暂时缺失（登记次序独立）；裁决时再报不存在。
	s.commits[c.ID] = &c
	return nil
}

func (s *store) getTag(id string) (*Tag, bool) {
	t, ok := s.tags[id]
	return t, ok
}

func (s *store) addTag(t Tag) error {
	if _, dup := s.tags[t.ID]; dup {
		return ErrInvalidArgument
	}
	s.tags[t.ID] = &t
	return nil
}
