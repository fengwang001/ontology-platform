package docsync

// diagNode participates in two independent implicit treaps:
//
//   - S-view, ordered by (a,b,id): a is the range start in UTF-16 units.
//   - E-view, ordered by (b,a,id): b is the range end in UTF-16 units.
//
// The two endpoints are deliberately separate so an edit can translate starts
// and ends by different rules (at a shared insertion point starts move while
// ends stay) without one view corrupting the other.
//
// The treaps carry no lazy tags: an edit enumerates and rebuilds only nodes at
// or after the edit boundary (the suffix that could change); the prefix before
// the edit point is never enumerated. Invalidation uses an interval stabbing
// search supported by the S-view maxB aggregate and is output-sensitive in the
// number of invalidated diagnostics.
type diagNode struct {
	id int
	a  int // range start (UTF-16 units)
	b  int // range end (UTF-16 units)

	diag Diagnostic

	spriority     uint64
	sleft, sright *diagNode
	ssize         int
	maxB          int // maximum b in this S-subtree

	epriority     uint64
	eleft, eright *diagNode
	esize         int
}

type diagSet struct {
	sroot, eroot *diagNode
	nextID       int
	counter      func()
}

func newDiagSet(counter func()) *diagSet {
	return &diagSet{nextID: 1, counter: counter}
}

func (ds *diagSet) touch(n *diagNode) {
	if n != nil && ds.counter != nil {
		ds.counter()
	}
}

const (
	minInt = -1 << 62
	maxInt = 1 << 62
)

// ---------------- S-view ----------------

func (n *diagNode) sPull() {
	n.ssize = 1
	n.maxB = n.b
	if n.sleft != nil {
		n.ssize += n.sleft.ssize
		if n.sleft.maxB > n.maxB {
			n.maxB = n.sleft.maxB
		}
	}
	if n.sright != nil {
		n.ssize += n.sright.ssize
		if n.sright.maxB > n.maxB {
			n.maxB = n.sright.maxB
		}
	}
}

func sKeyLess(ka, kb, kid int, o *diagNode) bool {
	if ka != o.a {
		return ka < o.a
	}
	if kb != o.b {
		return kb < o.b
	}
	return kid < o.id
}

func (ds *diagSet) sMerge(x, y *diagNode) *diagNode {
	if x == nil {
		return y
	}
	if y == nil {
		return x
	}
	if x.spriority > y.spriority {
		x.sright = ds.sMerge(x.sright, y)
		x.sPull()
		return x
	}
	y.sleft = ds.sMerge(x, y.sleft)
	y.sPull()
	return y
}

func (ds *diagSet) sSplit(t *diagNode, ka, kb, kid int) (l, r *diagNode) {
	if t == nil {
		return nil, nil
	}
	ds.touch(t)
	if sKeyLess(ka, kb, kid, t) {
		var ll *diagNode
		ll, t.sleft = ds.sSplit(t.sleft, ka, kb, kid)
		t.sPull()
		return ll, t
	}
	var rr *diagNode
	t.sright, rr = ds.sSplit(t.sright, ka, kb, kid)
	t.sPull()
	return t, rr
}

func (ds *diagSet) sEnum(t *diagNode, out *[]*diagNode) {
	if t == nil {
		return
	}
	ds.sEnum(t.sleft, out)
	ds.touch(t)
	*out = append(*out, t)
	ds.sEnum(t.sright, out)
}

// sBuild merges nodes already ordered by (a,b,id), adding da to starts only.
// Ends (b) are owned by the E-view which shifts them independently first.
func (ds *diagSet) sBuild(nodes []*diagNode, da int) *diagNode {
	var t *diagNode
	for _, n := range nodes {
		n.a += da
		n.sleft, n.sright = nil, nil
		n.ssize, n.maxB = 1, n.b
		t = ds.sMerge(t, n)
	}
	return t
}

// ---------------- E-view ----------------

func (n *diagNode) ePull() {
	n.esize = 1
	if n.eleft != nil {
		n.esize += n.eleft.esize
	}
	if n.eright != nil {
		n.esize += n.eright.esize
	}
}

func eKeyLess(kb, ka, kid int, o *diagNode) bool {
	if kb != o.b {
		return kb < o.b
	}
	if ka != o.a {
		return ka < o.a
	}
	return kid < o.id
}

func (ds *diagSet) eMerge(x, y *diagNode) *diagNode {
	if x == nil {
		return y
	}
	if y == nil {
		return x
	}
	if x.epriority > y.epriority {
		x.eright = ds.eMerge(x.eright, y)
		x.ePull()
		return x
	}
	y.eleft = ds.eMerge(x, y.eleft)
	y.ePull()
	return y
}

func (ds *diagSet) eSplit(t *diagNode, kb, ka, kid int) (l, r *diagNode) {
	if t == nil {
		return nil, nil
	}
	ds.touch(t)
	if eKeyLess(kb, ka, kid, t) {
		var ll *diagNode
		ll, t.eleft = ds.eSplit(t.eleft, kb, ka, kid)
		t.ePull()
		return ll, t
	}
	var rr *diagNode
	t.eright, rr = ds.eSplit(t.eright, kb, ka, kid)
	t.ePull()
	return t, rr
}

func (ds *diagSet) eEnum(t *diagNode, out *[]*diagNode) {
	if t == nil {
		return
	}
	ds.eEnum(t.eleft, out)
	ds.touch(t)
	*out = append(*out, t)
	ds.eEnum(t.eright, out)
}

// eBuild merges nodes already ordered by (b,a,id), adding db to b only.
func (ds *diagSet) eBuild(nodes []*diagNode, db int) *diagNode {
	var t *diagNode
	for _, n := range nodes {
		n.b += db
		n.eleft, n.eright = nil, nil
		n.esize = 1
		t = ds.eMerge(t, n)
	}
	return t
}

// ---------------- operations ----------------

func (ds *diagSet) insert(start, end int, d Diagnostic) *diagNode {
	n := &diagNode{
		id: ds.nextID, a: start, b: end, diag: d,
		spriority: nextPriority(), epriority: nextPriority(),
		ssize: 1, esize: 1, maxB: end,
	}
	ds.nextID++
	sl, sr := ds.sSplit(ds.sroot, n.a, n.b, n.id)
	ds.sroot = ds.sMerge(ds.sMerge(sl, n), sr)
	el, er := ds.eSplit(ds.eroot, n.b, n.a, n.id)
	ds.eroot = ds.eMerge(ds.eMerge(el, n), er)
	return n
}

// reportOverlapping appends nodes that must be invalidated by a positive-length
// edit [s,e): positive-length overlap (a<e && s<b && b>a) or an empty
// diagnostic strictly inside (s<a==b<e). maxB pruning skips subtrees whose
// intervals end no later than s, so cost is O((k+1) log n) for k reported.
func (ds *diagSet) reportOverlapping(t *diagNode, s, e int, out *[]*diagNode) {
	if t == nil || t.maxB <= s {
		return
	}
	ds.touch(t)
	positive := t.b > t.a && t.a < e && s < t.b
	emptyInside := t.a == t.b && s < t.a && t.a < e
	if positive || emptyInside {
		*out = append(*out, t)
	}
	if t.a >= e {
		// Node and its right subtree start at/after e: only left may overlap.
		ds.reportOverlapping(t.sleft, s, e, out)
		return
	}
	ds.reportOverlapping(t.sleft, s, e, out)
	ds.reportOverlapping(t.sright, s, e, out)
}

func (ds *diagSet) applyOneEdit(s, e, L int) []*diagNode {
	if s == e {
		ds.applyInsertion(s, L)
		return nil
	}
	return ds.applyReplacement(s, e, L)
}

// applyInsertion handles a zero-length insertion of L units at pos.
// Start mapping: a<pos stays; a==pos and a>pos move by L (empty at pos moves).
// End mapping: b<pos stays; b==pos stays for non-empty diags but moves for the
// empty diag that also moved its start; b>pos moves by L.
func (ds *diagSet) applyInsertion(pos, L int) {
	// E-view first while both coordinates are still pre-edit.
	// b<pos stays; b==pos: empty diag (a==b==pos) moves, non-empty stays;
	// b>pos moves.
	el, rest := ds.eSplit(ds.eroot, pos, minInt, minInt)
	at, after := ds.eSplit(rest, pos+1, minInt, minInt)
	var atNodes, stayNodes, movedNodes []*diagNode
	ds.eEnum(at, &atNodes)
	for _, n := range atNodes {
		if n.a == pos && n.b == pos {
			movedNodes = append(movedNodes, n)
		} else {
			stayNodes = append(stayNodes, n)
		}
	}
	ds.eEnum(after, &movedNodes)
	ds.eroot = ds.eMerge(el,
		ds.eMerge(ds.eBuild(stayNodes, 0), ds.eBuild(movedNodes, L)))

	// S-view: a<pos stays; a>=pos moves (empty at pos included).
	sl, sr := ds.sSplit(ds.sroot, pos, minInt, minInt)
	var right []*diagNode
	ds.sEnum(sr, &right)
	ds.sroot = ds.sMerge(sl, ds.sBuild(right, L))
}

// applyReplacement handles edit [s,e) replaced by L units (delta = L-(e-s)).
func (ds *diagSet) applyReplacement(s, e, L int) []*diagNode {
	delta := L - (e - s)

	var badNodes []*diagNode
	ds.reportOverlapping(ds.sroot, s, e, &badNodes)
	bad := make(map[int]bool, len(badNodes))
	for _, n := range badNodes {
		bad[n.id] = true
	}

	// Start mapping for survivors:
	//   a<s -> unchanged
	//   a==s -> only survivor is the empty diag at s; it excludes the edited
	//           content, so it stays at s (a pure replacement, not an insertion)
	//   a>=e -> shifted by delta (touching start at e included)
	var sBefore, sAtS, sAfter []*diagNode
	var allS []*diagNode
	ds.sEnum(ds.sroot, &allS)
	for _, n := range allS {
		if bad[n.id] {
			continue
		}
		switch {
		case n.a < s:
			sBefore = append(sBefore, n)
		case n.a == s:
			sAtS = append(sAtS, n)
		default:
			sAfter = append(sAfter, n)
		}
	}
	tree := ds.sBuild(sBefore, 0)
	tree = ds.sMerge(tree, ds.sBuild(sAtS, 0))
	tree = ds.sMerge(tree, ds.sBuild(sAfter, delta))
	ds.sroot = tree

	// End mapping for survivors:
	//   b<=s -> unchanged (empty diag at s keeps b at s)
	//   b>=e -> shifted by delta (empty diag at e included)
	// There is no survivor with s<b<e.
	var eBefore, eAfter []*diagNode
	var allE []*diagNode
	ds.eEnum(ds.eroot, &allE)
	for _, n := range allE {
		if bad[n.id] {
			continue
		}
		if n.b <= s {
			eBefore = append(eBefore, n)
		} else {
			eAfter = append(eAfter, n)
		}
	}
	ds.eroot = ds.eMerge(ds.eBuild(eBefore, 0), ds.eBuild(eAfter, delta))

	return badNodes
}

// batchEdit is one edit in a simultaneous batch at pre-batch coordinates.
type batchEdit struct{ s, e, L int }

// mapStart maps a start endpoint through ordered simultaneous edits. All
// membership tests use the point's ORIGINAL coordinate; the total translation
// is accumulated. For same-point insertions every insertion at or before the
// point contributes, so compositions at identical boundaries all apply.
func mapStartAll(orig int, eds []batchEdit) int {
	shift := 0
	x := orig
	for _, ed := range eds {
		switch {
		case ed.s == ed.e:
			if orig >= ed.s {
				shift += ed.L
			}
		case orig < ed.s:
			// before the edit: unchanged
		case orig == ed.s:
			// empty diag at a replacement start stays at s (start does not move)
		default: // orig > s
			if orig < ed.e {
				// positive start inside a deletion cannot survive (invalidated);
				// keep arithmetic well-defined anyway.
				shift += ed.L - (ed.e - ed.s)
			} else { // orig >= e
				shift += ed.L - (ed.e - ed.s)
			}
		}
		x = orig + shift
	}
	return x
}

// mapEnd maps an end endpoint through ordered simultaneous edits. empty marks
// an empty diagnostic, whose end at an insertion start moves like a start.
func mapEndAll(orig int, empty bool, eds []batchEdit) int {
	shift := 0
	x := orig
	for _, ed := range eds {
		switch {
		case ed.s == ed.e:
			switch {
			case orig < ed.s:
			case orig == ed.s && !empty:
				// non-empty range end at insertion point stays
			default:
				shift += ed.L
			}
		default:
			// Survivor ends are orig<=s (stay) or orig>=e (shift); an end
			// strictly inside is invalidated earlier.
			if orig >= ed.e {
				shift += ed.L - (ed.e - ed.s)
			}
		}
		x = orig + shift
	}
	return x
}

// applyBatch migrates diagnostics for a batch of non-overlapping simultaneous
// edits (sorted ascending by s, tie-broken by original order). Invalidation is
// computed against original coordinates; surviving endpoints are then mapped
// through all edits independently, which keeps same-point multi-insertions
// consistent and never visits diagnostics before the first boundary.
// applyBatch migrates diagnostics for non-overlapping simultaneous edits
// (sorted ascending by s, ties by original order). It processes edits
// right-to-left, so each edit's boundary needs no adjustment for edits to its
// right; diagnostics before the leftmost boundary are never visited, keeping
// work O((k+m) log n) for k invalidated and m moved diagnostics.
func (ds *diagSet) applyBatch(eds []batchEdit) []*diagNode {
	if len(eds) == 0 {
		return nil
	}
	// 1. Invalid union against original coordinates (interval-tree stabbing).
	invalid := map[int]*diagNode{}
	minS := -1
	for _, ed := range eds {
		if ed.s == ed.e {
			if minS < 0 || ed.s < minS {
				minS = ed.s
			}
			continue
		}
		if minS < 0 || ed.s < minS {
			minS = ed.s
		}
		var bad []*diagNode
		ds.reportOverlapping(ds.sroot, ed.s, ed.e, &bad)
		for _, n := range bad {
			invalid[n.id] = n
		}
	}

	// 2. Enumerate only nodes whose start is at/after minS (the region that can
	//    change). Spanning invalid nodes (start<minS) are removed by id without
	//    enumerating the prefix.
	preS, sufS := ds.sSplit(ds.sroot, minS, minInt, minInt)
	var span []*diagNode
	for _, n := range invalid {
		if n.a < minS {
			span = append(span, n)
		}
	}
	preS = ds.sRemoveIDs(preS, span)

	var sufNodes []*diagNode
	ds.sEnum(sufS, &sufNodes)
	var survivors []*diagNode
	for _, n := range sufNodes {
		if _, gone := invalid[n.id]; !gone {
			survivors = append(survivors, n)
		}
	}

	// 3. The set of nodes that can change is the union of the S suffix
	//    (start >= minS) and the E suffix (end > minS); a node with an earlier
	//    start but a crossing end only needs its end translated. Map each such
	//    surviving node's endpoints independently through all ORIGINAL edits.
	preE, sufE := ds.eSplit(ds.eroot, minS+1, minInt, minInt)
	preE = ds.eRemoveIDs(preE, span)
	var efNodes []*diagNode
	ds.eEnum(sufE, &efNodes)

	changed := map[int]*diagNode{}
	var changedList []*diagNode
	for _, n := range survivors {
		changed[n.id] = n
		changedList = append(changedList, n)
	}
	var keepE []*diagNode
	for _, n := range efNodes {
		if _, gone := invalid[n.id]; gone {
			continue
		}
		keepE = append(keepE, n)
		if _, seen := changed[n.id]; !seen {
			changed[n.id] = n
			changedList = append(changedList, n)
		}
	}
	for _, n := range changedList {
		empty := n.a == n.b
		if n.a >= minS {
			n.a = mapStartAll(n.a, eds)
		}
		n.b = mapEndAll(n.b, empty, eds)
	}

	sortDiagNodes(survivors, func(x, y *diagNode) bool {
		if x.a != y.a {
			return x.a < y.a
		}
		if x.b != y.b {
			return x.b < y.b
		}
		return x.id < y.id
	})
	ds.sroot = ds.sMerge(preS, ds.sRebuildRaw(survivors))

	sortDiagNodes(keepE, func(x, y *diagNode) bool {
		if x.b != y.b {
			return x.b < y.b
		}
		if x.a != y.a {
			return x.a < y.a
		}
		return x.id < y.id
	})
	ds.eroot = ds.eMerge(preE, ds.eRebuildRaw(keepE))

	out := make([]*diagNode, 0, len(invalid))
	for _, n := range invalid {
		out = append(out, n)
	}
	return out
}

func (ds *diagSet) applyReplacementFast(s, e, L int, invalid map[int]*diagNode) []*diagNode {
	delta := L - (e - s)

	// Find all overlapping intervals, including those starting before s and
	// spanning into the edit (interval-tree stabbing, output-sensitive).
	var spanning []*diagNode
	ds.reportOverlapping(ds.sroot, s, e, &spanning)
	var newlyDead []*diagNode
	for _, n := range spanning {
		if _, seen := invalid[n.id]; !seen {
			invalid[n.id] = n
			newlyDead = append(newlyDead, n)
		}
	}

	preS, restS := ds.sSplit(ds.sroot, s, minInt, minInt)
	atS, r1 := ds.sSplit(restS, s+1, minInt, minInt)
	inS, r2 := ds.sSplit(r1, e, minInt, minInt)
	atE, postE := ds.sSplit(r2, e+1, minInt, minInt)

	var atSNodes, inSNodes []*diagNode
	ds.sEnum(atS, &atSNodes)
	ds.sEnum(inS, &inSNodes)
	var emptyAtS []*diagNode
	for _, n := range atSNodes {
		if n.a == n.b {
			emptyAtS = append(emptyAtS, n)
		}
	}
	_ = inSNodes
	emptySTree := ds.sRebuildRaw(emptyAtS)
	if atE != nil {
		atE.shiftSBoth(delta)
	}
	if postE != nil {
		postE.shiftSBoth(delta)
	}
	preS = ds.sFilterTree(preS, invalid)
	ds.sroot = ds.sMerge(preS, ds.sMerge(emptySTree, ds.sMerge(atE, postE)))

	// E-view: nodes with end <= s are untouched and stay in preE; only end > s
	// nodes are enumerated.
	preE, restE := ds.eSplit(ds.eroot, s+1, minInt, minInt)
	var moved []*diagNode
	ds.eEnum(restE, &moved)
	var shiftE []*diagNode
	for _, n := range moved {
		if _, gone := invalid[n.id]; gone {
			continue
		}
		shiftE = append(shiftE, n)
	}
	// b was already translated by the S-view suffix shift on the same nodes;
	// here we only rebuild E-view ordering, we do not shift again.
	preE = ds.eFilterTree(preE, invalid)
	shiftTree := ds.eRebuildRaw(shiftE)
	ds.eroot = ds.eMerge(preE, shiftTree)
	return newlyDead
}

// sRebuildRaw/eRebuildRaw rebuild a treap from already key-ordered nodes
// without changing their a/b.
// sRemoveIDs removes specific nodes from an S-view subtree by their keys.
func (ds *diagSet) sRemoveIDs(t *diagNode, ids []*diagNode) *diagNode {
	for _, n := range ids {
		l, r := ds.sSplit(t, n.a, n.b, n.id)
		_, r = ds.sSplit(r, n.a, n.b, n.id+1)
		t = ds.sMerge(l, r)
	}
	return t
}

// eRemoveIDs removes specific nodes from an E-view subtree by their keys.
func (ds *diagSet) eRemoveIDs(t *diagNode, ids []*diagNode) *diagNode {
	for _, n := range ids {
		l, r := ds.eSplit(t, n.b, n.a, n.id)
		_, r = ds.eSplit(r, n.b, n.a, n.id+1)
		t = ds.eMerge(l, r)
	}
	return t
}

func (ds *diagSet) sFilterTree(t *diagNode, bad map[int]*diagNode) *diagNode {
	var nodes []*diagNode
	ds.sEnum(t, &nodes)
	keep := nodes[:0]
	for _, n := range nodes {
		if _, gone := bad[n.id]; !gone {
			keep = append(keep, n)
		}
	}
	return ds.sRebuildRaw(keep)
}

func (ds *diagSet) eFilterTree(t *diagNode, bad map[int]*diagNode) *diagNode {
	var nodes []*diagNode
	ds.eEnum(t, &nodes)
	keep := nodes[:0]
	for _, n := range nodes {
		if _, gone := bad[n.id]; !gone {
			keep = append(keep, n)
		}
	}
	return ds.eRebuildRaw(keep)
}

func (ds *diagSet) sRebuildRaw(nodes []*diagNode) *diagNode {
	var t *diagNode
	for _, n := range nodes {
		n.sleft, n.sright, n.ssize, n.maxB = nil, nil, 1, n.b
		t = ds.sMerge(t, n)
	}
	return t
}

func (ds *diagSet) eRebuildRaw(nodes []*diagNode) *diagNode {
	var t *diagNode
	for _, n := range nodes {
		n.eleft, n.eright, n.esize = nil, nil, 1
		t = ds.eMerge(t, n)
	}
	return t
}

func sortDiagNodes(nodes []*diagNode, less func(a, b *diagNode) bool) {
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0 && less(nodes[j], nodes[j-1]); j-- {
			nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
		}
	}
}

func (ds *diagSet) sRebuild(nodes []*diagNode) *diagNode {
	var t *diagNode
	for _, n := range nodes {
		n.sleft, n.sright, n.ssize, n.maxB = nil, nil, 1, n.b
		t = ds.sMerge(t, n)
	}
	return t
}

func (ds *diagSet) eRebuild(nodes []*diagNode) *diagNode {
	var t *diagNode
	for _, n := range nodes {
		n.eleft, n.eright, n.esize = nil, nil, 1
		t = ds.eMerge(t, n)
	}
	return t
}

func (ds *diagSet) activeSorted() []*diagNode {
	var out []*diagNode
	ds.sEnum(ds.sroot, &out)
	return out
}

// shiftSBoth adds d to a and b (and maxB) for every node in an S-subtree.
func (n *diagNode) shiftSBoth(d int) {
	n.a += d
	n.b += d
	n.maxB += d
	if n.sleft != nil {
		n.sleft.shiftSBoth(d)
	}
	if n.sright != nil {
		n.sright.shiftSBoth(d)
	}
}

// shiftE adds d to b for every node in an E-subtree.
func (n *diagNode) shiftE(d int) {
	n.b += d
	if n.eleft != nil {
		n.eleft.shiftE(d)
	}
	if n.eright != nil {
		n.eright.shiftE(d)
	}
}

// ---------------- invalidated list keyed by (version,id) ----------------

type invalNode struct {
	version  int64
	id       int64
	entry    InvalidatedEntry
	priority uint64
	left     *invalNode
	right    *invalNode
}

type invalidList struct{ root *invalNode }

func newInvalidList() *invalidList { return &invalidList{} }

func iMerge(a, b *invalNode) *invalNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.priority > b.priority {
		a.right = iMerge(a.right, b)
		return a
	}
	b.left = iMerge(a, b.left)
	return b
}

func iKeyLess(version int64, id int64, o *invalNode) bool {
	if version != o.version {
		return version < o.version
	}
	return id < o.id
}

func iSplit(t *invalNode, version int64, id int64) (a, b *invalNode) {
	if t == nil {
		return nil, nil
	}
	if iKeyLess(version, id, t) {
		var ll *invalNode
		ll, t.left = iSplit(t.left, version, id)
		return ll, t
	}
	var rr *invalNode
	t.right, rr = iSplit(t.right, version, id)
	return t, rr
}

func (l *invalidList) add(e InvalidatedEntry) {
	n := &invalNode{
		version: e.InvalidatedVersion, id: e.RegisteredAt,
		entry: e, priority: nextPriority(),
	}
	a, b := iSplit(l.root, n.version, n.id)
	l.root = iMerge(iMerge(a, n), b)
}

func iEnum(n *invalNode, out *[]InvalidatedEntry) {
	if n == nil {
		return
	}
	iEnum(n.left, out)
	*out = append(*out, n.entry)
	iEnum(n.right, out)
}

func (l *invalidList) sorted() []InvalidatedEntry {
	var out []InvalidatedEntry
	iEnum(l.root, &out)
	return out
}
