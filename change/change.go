package change

import "bytes"

type Op interface {
	isOp()
}

type Put struct {
	Value int64
}

type Delete struct{}

func (Put) isOp() {}

func (Delete) isOp() {}

func NewPut(value int64) Put { return Put{Value: value} }

var Del = Delete{}

type Triple struct {
	TS     int64
	Origin int
	Seq    int64
}

type Change struct {
	Origin int
	Seq    int64
	TS     int64
	Key    []byte
	Op     Op
}

type Entry struct {
	Triple Triple
	Value  int64
	Delete bool
	Exists bool
}

func (t Triple) Greater(other Triple) bool {
	if t.TS != other.TS {
		return t.TS > other.TS
	}
	if t.Origin != other.Origin {
		return t.Origin > other.Origin
	}
	return t.Seq > other.Seq
}

func (c Change) triple() Triple {
	return Triple{TS: c.TS, Origin: c.Origin, Seq: c.Seq}
}

type Table struct {
	records map[string]Entry
}

func NewTable() *Table { return &Table{} }

func (t *Table) Apply(c Change) bool {
	key := string(c.Key)
	current, exists := t.records[key]
	incoming := Entry{Triple: c.triple(), Exists: true}
	switch op := c.Op.(type) {
	case Put:
		incoming.Value = op.Value
	case Delete:
		incoming.Delete = true
	default:
		panic("change: invalid operation")
	}
	if exists && !incoming.Triple.Greater(current.Triple) {
		return false
	}
	if t.records == nil {
		t.records = make(map[string]Entry)
	}
	t.records[key] = incoming
	return true
}

func (t *Table) Get(key []byte) (Entry, bool) {
	if t == nil || t.records == nil {
		return Entry{}, false
	}
	entry, ok := t.records[string(key)]
	return entry, ok
}

func (t *Table) Records() map[string]Entry {
	records := make(map[string]Entry)
	if t == nil || t.records == nil {
		return records
	}
	for key, entry := range t.records {
		records[key] = entry
	}
	return records
}

func EqualEntries(a, b Entry) bool {
	return a == b
}

func EqualChanges(a, b Change) bool {
	return a.Origin == b.Origin &&
		a.Seq == b.Seq &&
		a.TS == b.TS &&
		bytes.Equal(a.Key, b.Key) &&
		equalOps(a.Op, b.Op)
}

func equalOps(a, b Op) bool {
	switch av := a.(type) {
	case Put:
		bv, ok := b.(Put)
		return ok && av.Value == bv.Value
	case Delete:
		_, ok := b.(Delete)
		return ok
	default:
		return false
	}
}
