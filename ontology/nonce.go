package ontology

// Nonce issues the next one-time nonce (1, 2, 3, ...) and adds it to the
// pool. When the pool is full the smallest live nonce is evicted first.
// Both insertion and eviction are amortized O(1), independent of capacity.
// It always succeeds and is safe for concurrent use.
func (m *Machine) Nonce() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nonceSeq++
	n := m.nonceSeq
	if m.nonceInPool >= m.cfg.C {
		// Advance the eviction cursor past values no longer in the pool
		// (consumed or evicted); every cursor value is visited at most once.
		for {
			if _, ok := m.nonceSet[m.nonceEvict]; ok {
				delete(m.nonceSet, m.nonceEvict)
				m.nonceEvict++
				break
			}
			m.nonceEvict++
		}
	} else {
		m.nonceInPool++
	}
	m.nonceSet[n] = struct{}{}
	return n
}

// consumeNonce removes n from the pool if present; false means invalid.
func (m *Machine) consumeNonce(n int64) bool {
	if _, ok := m.nonceSet[n]; !ok {
		return false
	}
	delete(m.nonceSet, n)
	m.nonceInPool--
	return true
}
