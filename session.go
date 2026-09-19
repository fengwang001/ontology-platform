package ontology

import "sync/atomic"

// session is one traversal: an immutable snapshot of the store's key order
// taken when the traversal starts. Writes to the store never touch sessions,
// so ongoing traversals never block writers.
type session struct {
	id   uint64
	keys []string
	set  map[string]struct{}

	baseIns int64 // store insert counter at snapshot time
	baseDel int64 // store delete counter at snapshot time

	hwm atomic.Int64 // high-water mark: snapshot positions < hwm were scanned
}

// advanceHWM raises the high-water mark to pos (never lowers it).
func (sess *session) advanceHWM(pos int) {
	for {
		cur := sess.hwm.Load()
		if int64(pos) <= cur || sess.hwm.CompareAndSwap(cur, int64(pos)) {
			return
		}
	}
}
