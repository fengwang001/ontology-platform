package hpack

import "sync"

// ErrInvalidArgument is returned for rejected Encoder/constructor operations.
var ErrInvalidArgument = Error("invalid argument")

// Encoder is an HPACK-style header compressor with dynamic table size
// negotiation. Methods are safe for concurrent use.
type Encoder struct {
	mu        sync.Mutex
	table     dynTable
	limit     int
	base      int
	min       int
	sensitive map[string]bool
}

// NewEncoder constructs an encoder with initial capacity c0, peer-advertised
// limit l0 and a set of sensitive header names.
func NewEncoder(c0, l0 int, sensitive map[string]bool) (*Encoder, error) {
	if c0 < 1 || c0 > l0 || l0 > 65536 {
		return nil, ErrInvalidArgument
	}
	sens := make(map[string]bool, len(sensitive))
	for name := range sensitive {
		sens[name] = true
	}
	return &Encoder{
		table:     newDynTable(c0),
		limit:     l0,
		base:      c0,
		min:       c0,
		sensitive: sens,
	}, nil
}

// SetLimit updates the peer-advertised upper bound and shrinks the table when
// the new limit is below the current capacity.
func (e *Encoder) SetLimit(l int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if l < 1 || l > 65536 {
		return ErrInvalidArgument
	}
	e.limit = l
	if l < e.table.cap {
		e.table.setCapacity(l)
		e.trackMin(l)
	}
	return nil
}

// trackMin records the smallest capacity seen since the last Encode.
func (e *Encoder) trackMin(c int) {
	if c < e.min {
		e.min = c
	}
}

// Resize sets the capacity (must be within the current limit) and shrinks.
func (e *Encoder) Resize(c int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c < 1 || c > e.limit {
		return ErrInvalidArgument
	}
	e.table.setCapacity(c)
	e.trackMin(c)
	return nil
}

// Encode turns an ordered header list into a Block, mutating the dynamic
// table. An invalid header aborts the whole call without any state change.
func (e *Encoder) Encode(headers []Header) (Block, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, h := range headers {
		if !validName(h.Name) || !validValue(h.Value) {
			return Block{}, ErrInvalidArgument
		}
	}

	updates := e.buildUpdates()

	trial := e.table.clone()
	instrs := make([]Instruction, 0, len(headers))
	for _, h := range headers {
		if e.sensitive[h.Name] {
			nameIdx := trial.nameIndex(h.Name)
			name := h.Name
			if nameIdx != 0 {
				name = ""
			}
			instrs = append(instrs, LiteralNever{NameIdx: nameIdx, Name: name, Value: h.Value})
			continue
		}
		if idx := trial.exactIndex(h.Name, h.Value); idx != 0 {
			instrs = append(instrs, Indexed{Index: idx})
			continue
		}
		nameIdx := trial.nameIndex(h.Name)
		name := h.Name
		if nameIdx != 0 {
			name = ""
		}
		instrs = append(instrs, LiteralIndexed{NameIdx: nameIdx, Name: name, Value: h.Value})
		trial.insert(h.Name, h.Value)
	}

	e.table = trial
	block := Block{Updates: updates, Instrs: instrs}
	e.base = e.table.cap
	e.min = e.table.cap
	return block, nil
}

// buildUpdates produces the size-update prefix per the negotiation rules and
// reflects any resizes that already happened locally.
func (e *Encoder) buildUpdates() []int {
	cur := e.table.cap
	switch {
	case e.min < e.base && e.min < cur:
		return []int{e.min, cur}
	case cur != e.base:
		return []int{cur}
	default:
		return nil
	}
}

// Capacity reports the current capacity.
func (e *Encoder) Capacity() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.table.cap
}

// Entries returns a copy of the dynamic entries, newest first.
func (e *Encoder) Entries() []Header {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Header, len(e.table.entries))
	for i, en := range e.table.entries {
		out[i] = Header{Name: en.name, Value: en.value}
	}
	return out
}
