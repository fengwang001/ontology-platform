// Package ws decides lazily whether a run of spaces/tabs is trailing.
package ws

import "ontology/eol"

// Item is a decided event: either a kept literal byte or a line ending.
// Dropped trailing spaces/tabs are reported with Drop=true for mapping.
type Item struct {
	Orig int  // original offset
	Len  int  // original span length (2 for \r\n, else 1)
	Byte byte // rendered byte ('\n' for NL)
	NL   bool // logical line ending
	Drop bool // trailing whitespace removed
}

// Decider buffers spaces/tabs until a non-space, a line ending, or EOF.
type Decider struct {
	buf []Item // undecided spaces/tabs
}

// Push feeds one eol event and appends decided items to out.
func (d *Decider) Push(ev eol.Event, out []Item) []Item {
	switch {
	case ev.Kind == eol.NL:
		d.buf = markDrop(d.buf)
		out = append(out, d.buf...)
		d.buf = d.buf[:0]
		return append(out, Item{Orig: ev.Orig, Len: ev.Len, Byte: '\n', NL: true})
	case ev.Byte == ' ' || ev.Byte == '\t':
		d.buf = append(d.buf, Item{Orig: ev.Orig, Len: 1, Byte: ev.Byte})
		return out
	default:
		out = append(out, d.buf...)
		d.buf = d.buf[:0]
		return append(out, Item{Orig: ev.Orig, Len: ev.Len, Byte: ev.Byte})
	}
}

// Flush decides buffered spaces at stream end as trailing and drops them.
func (d *Decider) Flush(out []Item) []Item {
	d.buf = markDrop(d.buf)
	out = append(out, d.buf...)
	d.buf = d.buf[:0]
	return out
}

// FlushRaw keeps buffered spaces verbatim (fragment mode: verdict deferred).
func (d *Decider) FlushRaw(out []Item) []Item {
	out = append(out, d.buf...)
	d.buf = d.buf[:0]
	return out
}

// Pending returns the undecided whitespace items (caller must not mutate).
func (d *Decider) Pending() []Item { return d.buf }

func markDrop(items []Item) []Item {
	for i := range items {
		items[i].Drop = true
	}
	return items
}
