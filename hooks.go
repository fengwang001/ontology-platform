package ontology

// HookCtx is the read-only view handed to pre-execution hooks. Hooks see
// every modification already made inside the transaction (including those
// of outer actions) but must never write.
type HookCtx struct {
	tx     *Tx
	index  int
	params map[string]any
}

// Params returns the normalized parameters of the action being checked.
func (h *HookCtx) Params() map[string]any {
	out := make(map[string]any, len(h.params))
	for k, v := range h.params {
		out[k] = v
	}
	return out
}

// ActionName returns the name of the action being checked.
func (h *HookCtx) ActionName() string {
	if len(h.tx.chain) == 0 {
		return ""
	}
	return h.tx.chain[len(h.tx.chain)-1]
}

// GetObject reads one object from the transactional state.
func (h *HookCtx) GetObject(id string) (Object, bool) {
	return h.tx.store.GetObject(id)
}

// ListObjects reads all objects from the transactional state.
func (h *HookCtx) ListObjects() []Object {
	return h.tx.store.ListObjects()
}

// RelationsOf reads all relations touching id.
func (h *HookCtx) RelationsOf(id string) []Relation {
	return h.tx.store.RelationsOf(id)
}

// RawStore exposes the underlying store as an escape hatch. Writing through
// it from a hook is a contract violation: the engine detects the write and
// fails the whole action with *HookViolationError.
func (h *HookCtx) RawStore() *Store {
	return h.tx.store
}
