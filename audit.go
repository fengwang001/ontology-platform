package gradeaudit

func (e *Engine) AuditLog() []AuditEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]AuditEntry, len(e.audit))
	copy(out, e.audit)
	return out
}

func (e *Engine) Clock() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}
