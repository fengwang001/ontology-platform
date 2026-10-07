package history

// Entry is a single session-history entry. The page state object lives
// only on the entry, never on the cached document, so a restore always
// observes the entry's current state.
type Entry struct {
	URL   string
	DocID uint64
	State any
	Seq   uint64
}

// entryList is the ordered entry list plus the current position.
// Locating a traversal target and deciding same-document are O(1):
// index arithmetic plus a map lookup.
type entryList struct {
	entries []Entry
	pos     int
	nextSeq uint64
	byDoc   map[uint64][]int // docID -> ascending entry indices
}

func newEntryList() *entryList {
	return &entryList{pos: -1, nextSeq: 1, byDoc: make(map[uint64][]int)}
}

func (l *entryList) current() (Entry, bool) {
	if l.pos < 0 {
		return Entry{}, false
	}
	return l.entries[l.pos], true
}

func (l *entryList) at(i int) Entry { return l.entries[i] }

// push truncates every entry after the current position and appends a
// new current entry. Returns the new entry and the truncated suffix.
func (l *entryList) push(url string, docID uint64, state any) (Entry, []Entry) {
	truncated := append([]Entry(nil), l.entries[l.pos+1:]...)
	for _, e := range truncated {
		idxs := l.byDoc[e.DocID]
		idxs = idxs[:len(idxs)-1] // truncated indices are a suffix of each doc's list
		if len(idxs) == 0 {
			delete(l.byDoc, e.DocID)
		} else {
			l.byDoc[e.DocID] = idxs
		}
	}
	l.entries = l.entries[:l.pos+1]
	e := Entry{URL: url, DocID: docID, State: state, Seq: l.nextSeq}
	l.nextSeq++
	l.entries = append(l.entries, e)
	l.pos++
	l.byDoc[docID] = append(l.byDoc[docID], l.pos)
	return e, truncated
}

// replace changes only the current entry's URL and state object;
// sequence number, document and position are untouched.
func (l *entryList) replace(url string, state any) {
	l.entries[l.pos].URL = url
	l.entries[l.pos].State = state
}

// locate resolves a relative delta to an absolute index in O(1).
func (l *entryList) locate(delta int) (int, bool) {
	t := l.pos + delta
	if t < 0 || t >= len(l.entries) {
		return 0, false
	}
	return t, true
}

func (l *entryList) moveTo(i int) { l.pos = i }

// referenced reports whether any remaining entry points at docID. O(1).
func (l *entryList) referenced(docID uint64) bool { return len(l.byDoc[docID]) > 0 }

// reassignDoc rewrites every entry carrying oldID to newID.
func (l *entryList) reassignDoc(oldID, newID uint64) {
	for _, i := range l.byDoc[oldID] {
		l.entries[i].DocID = newID
	}
	l.byDoc[newID] = l.byDoc[oldID]
	delete(l.byDoc, oldID)
}
