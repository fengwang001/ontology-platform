package hpack

import "sync"

// Decoder decompresses Blocks produced by an Encoder. It is safe for
// concurrent use; calls are serialized by an internal mutex.
type Decoder struct {
	mu    sync.Mutex
	table dynTable
	cap   int
	limit int
}

// NewDecoder requires 1 <= c0 <= ld <= 65536.
func NewDecoder(c0, ld int) (*Decoder, error) {
	if c0 < 1 || c0 > ld || ld > maxLimit {
		return nil, ErrParam
	}
	return &Decoder{cap: c0, limit: ld}, nil
}

// Decode applies the block's table size updates and instructions. Any
// error rolls the dynamic table, capacity and output back to the state
// before the call; no headers are returned on error.
func (d *Decoder) Decode(b Block) ([]Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, u := range b.Updates {
		if u < 1 || u > d.limit {
			return nil, ErrUpdate
		}
	}
	savedEntries := d.table.snapshot()
	savedUsed := d.table.used
	savedCap := d.cap
	rollback := func(err error) ([]Entry, error) {
		d.table.entries = savedEntries
		d.table.used = savedUsed
		d.cap = savedCap
		return nil, err
	}
	for _, u := range b.Updates {
		d.cap = u
		d.table.evictTo(u)
	}
	var out []Entry
	for _, ins := range b.Instrs {
		switch ins.Kind {
		case KindIndexed:
			if ins.Index < 1 {
				return rollback(ErrIndex)
			}
			e, ok := d.table.lookup(ins.Index)
			if !ok {
				return rollback(ErrIndex)
			}
			out = append(out, e)
		case KindLiteralIndexed, KindLiteralNever:
			// Syntax is checked before index resolution.
			if ins.NameIdx < 0 {
				return rollback(ErrSyntax)
			}
			if ins.NameIdx > 0 && ins.Name != "" {
				return rollback(ErrSyntax)
			}
			if ins.NameIdx == 0 && !validName(ins.Name) {
				return rollback(ErrSyntax)
			}
			if !validValue(ins.Value) {
				return rollback(ErrSyntax)
			}
			name := ins.Name
			if ins.NameIdx > 0 {
				e, ok := d.table.lookup(ins.NameIdx)
				if !ok {
					return rollback(ErrIndex)
				}
				name = e.Name
			}
			if ins.Kind == KindLiteralIndexed {
				d.table.insert(name, ins.Value, d.cap)
			}
			out = append(out, Entry{Name: name, Value: ins.Value})
		default:
			return rollback(ErrSyntax)
		}
	}
	return out, nil
}

// Table returns a snapshot of the dynamic table, newest entry first.
func (d *Decoder) Table() []Entry {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.table.snapshot()
}

// Capacity returns the current dynamic table capacity.
func (d *Decoder) Capacity() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cap
}

// Used returns the sum of entry sizes currently in the dynamic table.
func (d *Decoder) Used() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.table.used
}
