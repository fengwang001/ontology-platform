package hpack

type entry struct {
	name  string
	value string
}

func entrySize(name, value string) int {
	return len(name) + len(value) + 32
}

type dynTable struct {
	entries []entry // index 0 is newest
	used    int
	cap     int
}

func newDynTable(c int) dynTable {
	return dynTable{entries: []entry{}, cap: c}
}

// setCapacity resizes the table to c, evicting oldest entries until the
// total size fits.
func (t *dynTable) setCapacity(c int) {
	t.cap = c
	t.evict()
}

// evict removes oldest entries while the table exceeds the capacity.
func (t *dynTable) evict() {
	for t.used > t.cap && len(t.entries) > 0 {
		oldest := t.entries[len(t.entries)-1]
		t.used -= entrySize(oldest.name, oldest.value)
		t.entries = t.entries[:len(t.entries)-1]
	}
}

// insert appends name/value as the newest entry after clearing or evicting
// per the rules: an entry larger than the capacity clears the whole table and
// is not added.
func (t *dynTable) insert(name, value string) {
	size := entrySize(name, value)
	if size > t.cap {
		t.entries = t.entries[:0]
		t.used = 0
		return
	}
	t.evictFor(size)
	t.entries = append(t.entries, entry{})
	copy(t.entries[1:], t.entries)
	t.entries[0] = entry{name: name, value: value}
	t.used += size
}

// evictFor removes oldest entries until size more would fit.
func (t *dynTable) evictFor(size int) {
	for t.used+size > t.cap && len(t.entries) > 0 {
		oldest := t.entries[len(t.entries)-1]
		t.used -= entrySize(oldest.name, oldest.value)
		t.entries = t.entries[:len(t.entries)-1]
	}
}

func validName(s string) bool {
	if len(s) < 1 || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == ':' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

func validValue(s string) bool {
	return len(s) <= 1024
}

var staticTable = []entry{
	{name: ":method", value: "GET"},
	{name: ":method", value: "POST"},
	{name: ":path", value: "/"},
	{name: ":scheme", value: "https"},
}

// staticExactIndex returns the 1-based static index of an exact match, or 0.
func staticExactIndex(name, value string) int {
	for i, e := range staticTable {
		if e.name == name && e.value == value {
			return i + 1
		}
	}
	return 0
}

// staticNameIndex returns the smallest 1-based static index matching name.
func staticNameIndex(name string) int {
	for i, e := range staticTable {
		if e.name == name {
			return i + 1
		}
	}
	return 0
}

// exactIndex returns the index of a full (name,value) match, static table
// first then the newest dynamic match, or 0 if none.
func (t *dynTable) exactIndex(name, value string) int {
	if i := staticExactIndex(name, value); i != 0 {
		return i
	}
	for i, e := range t.entries {
		if e.name == name && e.value == value {
			return len(staticTable) + 1 + i
		}
	}
	return 0
}

// nameIndex returns the smallest static index matching name, else the newest
// dynamic index, else 0.
func (t *dynTable) nameIndex(name string) int {
	if i := staticNameIndex(name); i != 0 {
		return i
	}
	for i, e := range t.entries {
		if e.name == name {
			return len(staticTable) + 1 + i
		}
	}
	return 0
}

// at resolves a 1-based combined index: 1..4 static, 5+ dynamic.
func (t *dynTable) at(index int) (entry, bool) {
	if index >= 1 && index <= len(staticTable) {
		return staticTable[index-1], true
	}
	di := index - len(staticTable) - 1
	if index >= len(staticTable)+1 && di < len(t.entries) {
		return t.entries[di], true
	}
	return entry{}, false
}

func (t *dynTable) snapshot() []entry {
	cp := make([]entry, len(t.entries))
	copy(cp, t.entries)
	return cp
}

func (t *dynTable) restore(entries []entry, cap int) {
	t.entries = make([]entry, len(entries))
	copy(t.entries, entries)
	t.used = 0
	for _, e := range t.entries {
		t.used += entrySize(e.name, e.value)
	}
	t.cap = cap
}

// clone returns an independent copy for building an Encode trial.
func (t dynTable) clone() dynTable {
	cp := make([]entry, len(t.entries))
	copy(cp, t.entries)
	t.entries = cp
	return t
}
