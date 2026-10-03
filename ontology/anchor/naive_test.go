package anchor

type naiveRefKind int

const (
	naiveBOS naiveRefKind = iota
	naiveEOS
	naiveChar
)

type naiveRef struct {
	kind     naiveRefKind
	char     int
	leftSide bool
}

type naiveAnchor struct {
	kind      anchorKind
	rangeKind RangeKind
	createdAt int
	point     naiveRef
	start     naiveRef
	end       naiveRef
}

type naiveModel struct {
	doc       []int
	nextChar  int
	rev       int
	anchors   map[int]*naiveAnchor
	removed   map[int]bool
	nextID    int
	live      int
	docs      [][]int
	snapshots map[int]map[int]*naiveAnchor
}

func newNaive(length int) *naiveModel {
	m := &naiveModel{
		anchors:   make(map[int]*naiveAnchor),
		removed:   make(map[int]bool),
		snapshots: make(map[int]map[int]*naiveAnchor),
	}
	for range make([]struct{}, length) {
		m.nextChar++
		m.doc = append(m.doc, m.nextChar)
	}
	m.snapshot()
	return m
}

func (m *naiveModel) snapshot() {
	docCopy := make([]int, len(m.doc))
	copy(docCopy, m.doc)
	anchorsCopy := make(map[int]*naiveAnchor, len(m.anchors))
	for id, a := range m.anchors {
		value := *a
		anchorsCopy[id] = &value
	}
	m.docs = append(m.docs, docCopy)
	m.snapshots[m.rev] = anchorsCopy
}

func (m *naiveModel) snapshotAnchors() {
	anchorsCopy := make(map[int]*naiveAnchor, len(m.anchors))
	for id, a := range m.anchors {
		value := *a
		anchorsCopy[id] = &value
	}
	m.snapshots[m.rev] = anchorsCopy
}

func naiveGapRef(doc []int, gap int, bias Bias) naiveRef {
	if bias == Left {
		if gap == 0 {
			return naiveRef{kind: naiveBOS}
		}
		return naiveRef{kind: naiveChar, char: doc[gap-1], leftSide: true}
	}
	if gap == len(doc) {
		return naiveRef{kind: naiveEOS}
	}
	return naiveRef{kind: naiveChar, char: doc[gap]}
}

func naiveRefPosition(doc []int, ref naiveRef) int {
	switch ref.kind {
	case naiveBOS:
		return 0
	case naiveEOS:
		return len(doc)
	}
	for index, char := range doc {
		if char == ref.char {
			if ref.leftSide {
				return index + 1
			}
			return index
		}
	}
	panic("naive character reference disappeared without remap")
}

func (m *naiveModel) replace(position, deleted, inserted int) {
	oldDoc := append([]int(nil), m.doc...)
	oldLength := len(oldDoc)
	deletedChars := make(map[int]bool)
	for _, char := range oldDoc[position : position+deleted] {
		deletedChars[char] = true
	}

	for _, a := range m.anchors {
		a.point = remapDeleted(a.point, deletedChars, position, deleted, oldLength, oldDoc)
		a.start = remapDeleted(a.start, deletedChars, position, deleted, oldLength, oldDoc)
		a.end = remapDeleted(a.end, deletedChars, position, deleted, oldLength, oldDoc)
	}

	newChars := make([]int, inserted)
	for i := range newChars {
		m.nextChar++
		newChars[i] = m.nextChar
	}
	updated := make([]int, 0, len(oldDoc)-deleted+inserted)
	updated = append(updated, oldDoc[:position]...)
	updated = append(updated, newChars...)
	updated = append(updated, oldDoc[position+deleted:]...)
	m.doc = updated
	m.rev++
	m.fixCollapsedRanges()
	m.snapshot()
}

func remapDeleted(ref naiveRef, deleted map[int]bool, position, deletedCount, oldLength int, oldDoc []int) naiveRef {
	if ref.kind != naiveChar {
		return ref
	}
	if !deleted[ref.char] {
		return ref
	}
	if ref.leftSide {
		if position == 0 {
			return naiveRef{kind: naiveBOS}
		}
		return naiveRef{kind: naiveChar, char: oldDoc[position-1], leftSide: true}
	}
	if position+deletedCount < oldLength {
		return naiveRef{kind: naiveChar, char: oldDoc[position+deletedCount]}
	}
	return naiveRef{kind: naiveEOS}
}

func (m *naiveModel) move(position, length, destinationArgument int) {
	block := append([]int(nil), m.doc[position:position+length]...)
	without := append(append([]int{}, m.doc[:position]...), m.doc[position+length:]...)
	destination := destinationArgument
	if destinationArgument > position+length {
		destination = destinationArgument - length
	}
	updated := make([]int, 0, len(m.doc))
	updated = append(updated, without[:destination]...)
	updated = append(updated, block...)
	updated = append(updated, without[destination:]...)
	m.doc = updated
	m.rev++
	m.fixCollapsedRanges()
	m.snapshot()
}

func (m *naiveModel) fixCollapsedRanges() {
	for _, a := range m.anchors {
		if a.kind != rangeAnchor {
			continue
		}
		start := naiveRefPosition(m.doc, a.start)
		end := naiveRefPosition(m.doc, a.end)
		if start > end {
			startBias, endBias := rangeBiases(a.rangeKind)
			a.start = naiveGapRef(m.doc, end, startBias)
			a.end = naiveGapRef(m.doc, end, endBias)
		}
	}
}

func (m *naiveModel) addPoint(position int, bias Bias) int {
	m.nextID++
	m.anchors[m.nextID] = &naiveAnchor{
		kind:      pointAnchor,
		createdAt: m.rev,
		point:     naiveGapRef(m.doc, position, bias),
	}
	m.live++
	m.snapshotAnchors()
	return m.nextID
}

func (m *naiveModel) addRange(start, end int, kind RangeKind) int {
	startBias, endBias := rangeBiases(kind)
	m.nextID++
	m.anchors[m.nextID] = &naiveAnchor{
		kind:      rangeAnchor,
		rangeKind: kind,
		createdAt: m.rev,
		start:     naiveGapRef(m.doc, start, startBias),
		end:       naiveGapRef(m.doc, end, endBias),
	}
	m.live++
	m.snapshotAnchors()
	return m.nextID
}

func (m *naiveModel) remove(id int) {
	m.removed[id] = true
	m.live--
}

func (m *naiveModel) point(id, rev int) int {
	return naiveRefPosition(m.docs[rev], m.snapshots[rev][id].point)
}

func (m *naiveModel) rangeResult(id, rev int) (int, int, bool) {
	a := m.snapshots[rev][id]
	start := naiveRefPosition(m.docs[rev], a.start)
	end := naiveRefPosition(m.docs[rev], a.end)
	return start, end, start == end
}

func errorIs(got, want error) bool {
	return got == want
}
