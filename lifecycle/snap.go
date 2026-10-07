package lifecycle

// snap 是存储内容的深拷贝视图，规划阶段的全部判定都在它上面进行，
// 因而判定复杂度只依赖本次迁移实际涉及的实例，与历史迁移总次数无关。
type snap struct {
	inst map[string]*Instance
}

// snapshot 返回全部实例当前版本的浅一致映射。
// 读端只做：在 RLock 下取 id 列表，再对每个 id 原子加载版本指针，
// 不触碰实例内部可变字段，因此与并发提交（互不相交实例）完全并行。
func (s *Store) snapshot() *snap {
	s.mu.RLock()
	ids := make([]string, 0, len(s.inst))
	for id := range s.inst {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	cp := &snap{
		inst: make(map[string]*Instance, len(s.inst)),
	}
	for _, id := range ids {
		s.mu.RLock()
		slot := s.inst[id]
		s.mu.RUnlock()
		if slot != nil {
			cp.inst[id] = slot.load()
		}
	}
	return cp
}

func cloneAttrs(a map[string]AttrValue) map[string]AttrValue {
	c := make(map[string]AttrValue, len(a))
	for k, v := range a {
		c[k] = v
	}
	return c
}
