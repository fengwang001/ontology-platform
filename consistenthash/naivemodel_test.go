package consistenthash_test

// naiveModel is a line-by-line reimplementation of the specification, kept
// deliberately separate from the production code so the differential test
// cannot share logic with the object under test.

type naiveKey struct {
	key  string
	pos  uint64
	seq  int64
	node int64
}

type naivePoint struct {
	pos  uint64
	node int64
}

type naiveModel struct {
	cnum, cden int64
	nodes      map[int64]map[uint64]bool // node id -> point set
	points     []naivePoint              // sorted by pos
	keys       map[string]*naiveKey
	next       int64

	log func(format string, args ...any)
}

func newNaiveModel(cnum, cden int64, log func(string, ...any)) *naiveModel {
	return &naiveModel{
		cnum:  cnum,
		cden:  cden,
		nodes: map[int64]map[uint64]bool{},
		keys:  map[string]*naiveKey{},
		log:   log,
	}
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

func (m *naiveModel) load(id int64) int {
	n := 0
	for _, k := range m.keys {
		if k.node == id {
			n++
		}
	}
	return n
}

// lowerBound returns the first sorted-point index with pos >= p.
func (m *naiveModel) lowerBound(p uint64) int {
	lo, hi := 0, len(m.points)
	for lo < hi {
		mid := (lo + hi) / 2
		if m.points[mid].pos < p {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(m.points) {
		lo = 0
	}
	return lo
}

// place walks clockwise from pos skipping nodes with load >= cap.
func (m *naiveModel) place(pos uint64, cap int64) int64 {
	start := m.lowerBound(pos)
	p := len(m.points)
	for step := 0; step < p; step++ {
		idx := (start + step) % p
		id := m.points[idx].node
		if int64(m.load(id)) < cap {
			return id
		}
	}
	panic("naive: no eligible node")
}

func (m *naiveModel) addNode(id int64, points []uint64) error {
	if id < 1 || len(points) < 1 || len(points) > 64 {
		return errNaiveInvalidNode
	}
	seen := map[uint64]bool{}
	for _, p := range points {
		if seen[p] {
			return errNaiveInvalidNode
		}
		seen[p] = true
	}
	if m.nodes[id] != nil {
		return errNaiveNodeExists
	}
	for _, p := range points {
		for _, pe := range m.points {
			if pe.pos == p {
				return errNaivePointConflict
			}
		}
	}
	set := map[uint64]bool{}
	for _, p := range points {
		set[p] = true
		m.points = append(m.points, naivePoint{pos: p, node: id})
	}
	m.nodes[id] = set
	m.resortPoints()
	return nil
}

func (m *naiveModel) resortPoints() {
	for i := 1; i < len(m.points); i++ {
		for j := i; j > 0 && m.points[j-1].pos > m.points[j].pos; j-- {
			m.points[j-1], m.points[j] = m.points[j], m.points[j-1]
		}
	}
}

func (m *naiveModel) put(key string, pos uint64) error {
	if key == "" {
		return errNaiveInvalidKey
	}
	if len(m.nodes) == 0 {
		return errNaiveNoNode
	}
	if m.keys[key] != nil {
		return errNaiveKeyExists
	}
	k := int64(len(m.keys))
	n := int64(len(m.nodes))
	cap := ceilDiv(m.cnum*(k+1), m.cden*n)
	id := m.place(pos, cap)
	m.next++
	m.keys[key] = &naiveKey{key: key, pos: pos, seq: m.next, node: id}
	return nil
}

func (m *naiveModel) del(key string) error {
	if key == "" {
		return errNaiveInvalidKey
	}
	if m.keys[key] == nil {
		return errNaiveKeyNotFound
	}
	delete(m.keys, key)
	return nil
}

func (m *naiveModel) lookup(key string) (int64, error) {
	if key == "" {
		return 0, errNaiveInvalidKey
	}
	k := m.keys[key]
	if k == nil {
		return 0, errNaiveKeyNotFound
	}
	return k.node, nil
}

func (m *naiveModel) removeNode(id int64) error {
	if m.nodes[id] == nil {
		return errNaiveNodeNotFound
	}
	if len(m.nodes) == 1 && m.load(id) > 0 {
		return errNaiveLastBusy
	}
	drop := m.nodes[id]
	kept := m.points[:0]
	for _, pe := range m.points {
		if !drop[pe.pos] {
			kept = append(kept, pe)
		}
	}
	m.points = kept
	delete(m.nodes, id)

	victims := []*naiveKey{}
	for _, k := range m.keys {
		if k.node == id {
			victims = append(victims, k)
		}
	}
	for i := 1; i < len(victims); i++ {
		for j := i; j > 0 && victims[j-1].seq > victims[j].seq; j-- {
			victims[j-1], victims[j] = victims[j], victims[j-1]
		}
	}
	for _, k := range victims {
		delete(m.keys, k.key)
	}

	n := int64(len(m.nodes))
	kc := int64(len(m.keys))
	for _, k := range victims {
		cap := ceilDiv(m.cnum*(kc+1), m.cden*n)
		k.node = m.place(k.pos, cap)
		m.keys[k.key] = k
		kc++
	}
	return nil
}

type naiveMigration struct {
	key  string
	from int64
	to   int64
}

func (m *naiveModel) rebalance(limit int) ([]naiveMigration, int, error) {
	if limit < 0 || limit > 1_000_000_000 {
		return nil, 0, errNaiveInvalidLimit
	}
	if len(m.nodes) == 0 {
		return nil, 0, errNaiveNoNode
	}
	k := int64(len(m.keys))
	n := int64(len(m.nodes))
	capR := ceilDiv(m.cnum*k, m.cden*n)

	ids := make([]int64, 0, len(m.nodes))
	for id := range m.nodes {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}

	migs := []naiveMigration{}
	moved := 0
loop:
	for _, id := range ids {
		for m.load(id) > int(capR) {
			if moved >= limit {
				break loop
			}
			var victim *naiveKey
			for _, kk := range m.keys {
				if kk.node == id && (victim == nil || kk.seq > victim.seq) {
					victim = kk
				}
			}
			delete(m.keys, victim.key) // detach first...
			newID := m.place(victim.pos, capR)
			victim.node = newID
			m.keys[victim.key] = victim
			migs = append(migs, naiveMigration{victim.key, id, newID})
			moved++
		}
	}
	excess := 0
	for _, id := range ids {
		if l := m.load(id); l > int(capR) {
			excess += l - int(capR)
		}
	}
	return migs, excess, nil
}

// Error stand-ins for the model; compared against the package errors by
// semantic position in the differential test.
type naiveErr string

func (e naiveErr) Error() string { return string(e) }

const (
	errNaiveInvalidKey    naiveErr = "invalid key"
	errNaiveInvalidNode   naiveErr = "invalid node"
	errNaiveNoNode        naiveErr = "no node"
	errNaiveKeyExists     naiveErr = "key exists"
	errNaiveKeyNotFound   naiveErr = "key not found"
	errNaiveNodeExists    naiveErr = "node exists"
	errNaivePointConflict naiveErr = "point conflict"
	errNaiveNodeNotFound  naiveErr = "node not found"
	errNaiveLastBusy      naiveErr = "last node busy"
	errNaiveInvalidLimit  naiveErr = "invalid limit"
)
