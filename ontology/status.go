package ontology

// IndexStatusFor 返回索引当前状态（供外部观测重建中/失败/可用）。
func (p *Platform) IndexStatusFor(t TypeID, a AttrName) (IndexStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.indexes[indexKey{t: t, a: a}]
	if !ok {
		return 0, false
	}
	return st.status, true
}
