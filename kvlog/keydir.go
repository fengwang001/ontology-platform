package kvlog

// locator is the keydir pointer to the newest record of a key.
type locator struct {
	segment int
	offset  int64
	length  int
	seq     uint64
	tomb    bool
}

// keyDir keeps one locator per key: the record with the largest seq.
// All access is serialized by the Engine lock.
type keyDir struct {
	m map[string]*locator
}

func newKeyDir() *keyDir { return &keyDir{m: make(map[string]*locator)} }

func (d *keyDir) len() int { return len(d.m) }

func (d *keyDir) get(key string) (*locator, bool) {
	l, ok := d.m[key]
	return l, ok
}

// put records l unless a locator with an equal or greater seq exists.
func (d *keyDir) put(key string, l locator) {
	cur, ok := d.m[key]
	if !ok || l.seq > cur.seq {
		lc := l
		d.m[key] = &lc
	}
}

// removeSegment drops every locator pointing at segment id.
func (d *keyDir) removeSegment(id int) int {
	n := 0
	for k, l := range d.m {
		if l.segment == id {
			delete(d.m, k)
			n++
		}
	}
	return n
}
