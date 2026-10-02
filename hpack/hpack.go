// Package hpack implements an HPACK-style header compression encoder and
// decoder with dynamic table size negotiation.
package hpack

import "errors"

var (
	ErrParam  = errors.New("hpack: invalid parameter")
	ErrUpdate = errors.New("hpack: invalid table size update")
	ErrIndex  = errors.New("hpack: invalid index")
	ErrSyntax = errors.New("hpack: syntax error")
)

const (
	maxLimit      = 65536
	entryOverhead = 32
	staticLen     = 4
)

type Entry struct {
	Name  string
	Value string
}

type InstrKind int

const (
	KindIndexed InstrKind = iota
	KindLiteralIndexed
	KindLiteralNever
)

type Instr struct {
	Kind    InstrKind
	Index   int
	NameIdx int
	Name    string
	Value   string
}

type Block struct {
	Updates []int
	Instrs  []Instr
}

var staticTable = [staticLen]Entry{
	{Name: ":method", Value: "GET"},
	{Name: ":method", Value: "POST"},
	{Name: ":path", Value: "/"},
	{Name: ":scheme", Value: "https"},
}

func entrySize(name, value string) int {
	return len(name) + len(value) + entryOverhead
}

func validName(s string) bool {
	if len(s) < 1 || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-', c == ':', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

func validValue(s string) bool {
	return len(s) <= 1024
}

// dynTable holds dynamic entries newest-first. Dynamic index 5 is the
// newest entry; older entries have larger indices.
type dynTable struct {
	entries []Entry
	used    int
}

func (t *dynTable) insert(name, value string, cap int) {
	size := entrySize(name, value)
	if size > cap {
		t.entries = nil
		t.used = 0
		return
	}
	for t.used+size > cap {
		t.evictOldest()
	}
	t.entries = append([]Entry{{Name: name, Value: value}}, t.entries...)
	t.used += size
}

func (t *dynTable) evictTo(cap int) {
	for t.used > cap {
		t.evictOldest()
	}
}

func (t *dynTable) evictOldest() {
	last := t.entries[len(t.entries)-1]
	t.entries = t.entries[:len(t.entries)-1]
	t.used -= entrySize(last.Name, last.Value)
}

func (t *dynTable) at(dynIdx int) (Entry, bool) {
	if dynIdx < 0 || dynIdx >= len(t.entries) {
		return Entry{}, false
	}
	return t.entries[dynIdx], true
}

// lookup resolves a 1-based global index: 1..4 static, 5+ dynamic.
func (t *dynTable) lookup(idx int) (Entry, bool) {
	if idx >= 1 && idx <= staticLen {
		return staticTable[idx-1], true
	}
	if idx >= staticLen+1 {
		return t.at(idx - staticLen - 1)
	}
	return Entry{}, false
}

// findFull returns the global index of an exact (name, value) match:
// static table first, then the newest dynamic match. 0 means no match.
func (t *dynTable) findFull(name, value string) int {
	for i, e := range staticTable {
		if e.Name == name && e.Value == value {
			return i + 1
		}
	}
	for i, e := range t.entries {
		if e.Name == name && e.Value == value {
			return staticLen + 1 + i
		}
	}
	return 0
}

// findName returns the global index of a name-only match: smallest static
// index first, then the newest dynamic match. 0 means no match.
func (t *dynTable) findName(name string) int {
	for i, e := range staticTable {
		if e.Name == name {
			return i + 1
		}
	}
	for i, e := range t.entries {
		if e.Name == name {
			return staticLen + 1 + i
		}
	}
	return 0
}

func (t *dynTable) snapshot() []Entry {
	out := make([]Entry, len(t.entries))
	copy(out, t.entries)
	return out
}
