package kvlog

// forceSeal seals the current active segment (test helper).
func (e *Engine) forceSeal() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sealActiveLocked("test")
}

func (e *Engine) segIDs() []int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ids := make([]int, 0, len(e.segs))
	for _, s := range e.segs {
		ids = append(ids, s.id)
	}
	return ids
}
