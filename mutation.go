package ontology

func (m *Manager) commit(_ []VMA, next []VMA, _, _ int64) {
	m.replaceAll(next)
}
