package hpack

import "sync"

// Encoder compresses header lists into Blocks. It is safe for concurrent
// use; calls are serialized by an internal mutex, so concurrent results
// equal some serial execution order.
type Encoder struct {
	mu        sync.Mutex
	table     dynTable
	cap       int
	limit     int
	base      int
	min       int
	sensitive map[string]bool
}

// NewEncoder requires 1 <= c0 <= l0 <= 65536. sensitive lists header names
// (exact match) that are always encoded as LiteralNever and never inserted
// into the dynamic table.
func NewEncoder(c0, l0 int, sensitive []string) (*Encoder, error) {
	if c0 < 1 || c0 > l0 || l0 > maxLimit {
		return nil, ErrParam
	}
	sens := make(map[string]bool, len(sensitive))
	for _, s := range sensitive {
		sens[s] = true
	}
	return &Encoder{cap: c0, limit: l0, base: c0, min: c0, sensitive: sens}, nil
}

// SetLimit updates the peer-advertised limit. If l is below the current
// capacity, the capacity is lowered immediately and the table is shrunk.
func (e *Encoder) SetLimit(l int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if l < 1 || l > maxLimit {
		return ErrParam
	}
	e.limit = l
	if l < e.cap {
		e.cap = l
		e.table.evictTo(l)
	}
	e.trackMin()
	return nil
}

// Resize sets the current capacity and shrinks the table if needed.
func (e *Encoder) Resize(c int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c < 1 || c > e.limit {
		return ErrParam
	}
	e.cap = c
	e.table.evictTo(c)
	e.trackMin()
	return nil
}

func (e *Encoder) trackMin() {
	if e.cap < e.min {
		e.min = e.cap
	}
}

// Encode compresses headers (in order) into a Block. If any header is
// invalid the call fails with ErrParam and no state changes.
func (e *Encoder) Encode(headers []Entry) (Block, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range headers {
		if !validName(h.Name) || !validValue(h.Value) {
			return Block{}, ErrParam
		}
	}
	var updates []int
	if e.min < e.base && e.min < e.cap {
		updates = []int{e.min, e.cap}
	} else if e.cap != e.base {
		updates = []int{e.cap}
	}
	var instrs []Instr
	for _, h := range headers {
		if e.sensitive[h.Name] {
			instrs = append(instrs, literal(KindLiteralNever, e.table.findName(h.Name), h))
			continue
		}
		if idx := e.table.findFull(h.Name, h.Value); idx > 0 {
			instrs = append(instrs, Instr{Kind: KindIndexed, Index: idx})
			continue
		}
		nameIdx := e.table.findName(h.Name)
		instrs = append(instrs, literal(KindLiteralIndexed, nameIdx, h))
		e.table.insert(h.Name, h.Value, e.cap)
	}
	e.base = e.cap
	e.min = e.cap
	return Block{Updates: updates, Instrs: instrs}, nil
}

// literal builds a literal instruction; the literal name is carried only
// when nameIdx is 0.
func literal(kind InstrKind, nameIdx int, h Entry) Instr {
	ins := Instr{Kind: kind, NameIdx: nameIdx, Value: h.Value}
	if nameIdx == 0 {
		ins.Name = h.Name
	}
	return ins
}

// Table returns a snapshot of the dynamic table, newest entry first.
func (e *Encoder) Table() []Entry {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.table.snapshot()
}

// Capacity returns the current dynamic table capacity.
func (e *Encoder) Capacity() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cap
}

// Used returns the sum of entry sizes currently in the dynamic table.
func (e *Encoder) Used() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.table.used
}
