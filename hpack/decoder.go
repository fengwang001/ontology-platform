package hpack

import "sync"

// Decoder is the peer of Encoder. Methods are safe for concurrent use.
type Decoder struct {
	mu    sync.Mutex
	table dynTable
	limit int
}

// NewDecoder constructs a decoder with initial capacity c0 and local
// advertised limit ld.
func NewDecoder(c0, ld int) (*Decoder, error) {
	if c0 < 1 || c0 > ld || ld > 65536 {
		return nil, ErrInvalidArgument
	}
	return &Decoder{table: newDynTable(c0), limit: ld}, nil
}

// Decode applies a Block atomically: on error the dynamic table, capacity and
// returned headers are all rolled back to the pre-call state.
func (d *Decoder) Decode(b Block) ([]Header, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	savedEntries := d.table.snapshot()
	savedCap := d.table.cap
	rollback := func() {
		d.table.restore(savedEntries, savedCap)
	}

	for _, v := range b.Updates {
		if v < 1 || v > d.limit {
			rollback()
			return nil, ErrUpdate
		}
	}

	trial := d.table.clone()
	for _, v := range b.Updates {
		trial.setCapacity(v)
	}

	headers := make([]Header, 0, len(b.Instrs))
	for _, ins := range b.Instrs {
		switch p := ins.(type) {
		case Indexed:
			e, ok := trial.at(p.Index)
			if !ok {
				rollback()
				return nil, ErrIndex
			}
			headers = append(headers, Header{Name: e.name, Value: e.value})
		case LiteralIndexed:
			name, err := resolveLiteralName(&trial, p.NameIdx, p.Name, p.Value)
			if err != nil {
				rollback()
				return nil, err
			}
			headers = append(headers, Header{Name: name, Value: p.Value})
			trial.insert(name, p.Value)
		case LiteralNever:
			name, err := resolveLiteralName(&trial, p.NameIdx, p.Name, p.Value)
			if err != nil {
				rollback()
				return nil, err
			}
			headers = append(headers, Header{Name: name, Value: p.Value})
		default:
			rollback()
			return nil, ErrSyntax
		}
	}

	d.table = trial
	return headers, nil
}

// resolveLiteralName validates a literal's name against the table snapshot
// before the instruction's own insertion, and also validates the value.
// ErrSyntax (bad literal form or invalid name/value bytes) takes precedence
// over ErrIndex.
func resolveLiteralName(t *dynTable, nameIdx int, literal, value string) (string, error) {
	if nameIdx == 0 {
		if !validName(literal) {
			return "", ErrSyntax
		}
	} else {
		if literal != "" {
			return "", ErrSyntax
		}
	}
	if !validValue(value) {
		return "", ErrSyntax
	}
	if nameIdx == 0 {
		return literal, nil
	}
	e, ok := t.at(nameIdx)
	if !ok {
		return "", ErrIndex
	}
	return e.name, nil
}

// Capacity reports the current capacity.
func (d *Decoder) Capacity() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.table.cap
}

// Entries returns a copy of the dynamic entries, newest first.
func (d *Decoder) Entries() []Header {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Header, len(d.table.entries))
	for i, en := range d.table.entries {
		out[i] = Header{Name: en.name, Value: en.value}
	}
	return out
}
