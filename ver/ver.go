// Package ver holds the per-key version primitive: a transaction's
// uncommitted (pending) write set and a key's committed state.
//
// By the project rules every key keeps only its latest committed version
// (the one carrying the greatest commit sequence), so the "version chain"
// collapses to a single Entry node. The package depends on nothing else.
package ver

// Pending is one transaction's uncommitted write set: key -> value.
type Pending struct {
	m map[string]string
}

// NewPending returns an empty pending write set.
func NewPending() *Pending {
	return &Pending{m: make(map[string]string)}
}

// Put records (k, v) as a pending write. A later Put to the same key
// overwrites the earlier pending value.
func (p *Pending) Put(k, v string) {
	p.m[k] = v
}

// Get returns the pending value for k, if any.
func (p *Pending) Get(k string) (string, bool) {
	v, ok := p.m[k]
	return v, ok
}

// Len returns the number of pending writes.
func (p *Pending) Len() int { return len(p.m) }

// Range calls f once for every pending (k, v) pair in unspecified order.
func (p *Pending) Range(f func(k, v string)) {
	for k, v := range p.m {
		f(k, v)
	}
}

// Entry is the committed state of one key: its single latest committed
// version plus the commit sequence that installed it.
type Entry struct {
	value string
	seq   int
	set   bool
}

// Apply installs value v committed at sequence seq. When a smaller
// sequence arrives out of order it is ignored: only the greatest seq
// survives, i.e. only the latest committed version is retained.
func (e *Entry) Apply(v string, seq int) {
	if !e.set || seq > e.seq {
		e.value = v
		e.seq = seq
		e.set = true
	}
}

// Latest returns the latest committed value. ok is false when the key has
// never been committed ("not exists", distinct from an empty value).
func (e Entry) Latest() (value string, ok bool) {
	return e.value, e.set
}
