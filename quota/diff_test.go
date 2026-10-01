package quota

import (
	"errors"
	"math/rand"
	"testing"
)

// Naive reference model: every quantity is recomputed from scratch from the
// file/directory structure and per-directory reserves.

type refNode struct {
	alive        bool
	isDir        bool
	parent       ID
	size         int64
	reserve      int64
	bytesQuota   int64
	entriesQuota int64
	children     []ID
}

type refModel struct {
	nodes []*refNode
}

type refErr struct {
	cat string
	dir ID
}

func newRef() *refModel {
	return &refModel{nodes: []*refNode{{
		alive: true, isDir: true, parent: 0,
		bytesQuota: -1, entriesQuota: -1, children: []ID{},
	}}}
}

func (m *refModel) clone() *refModel {
	c := &refModel{nodes: make([]*refNode, len(m.nodes))}
	for i, n := range m.nodes {
		cp := *n
		cp.children = append([]ID(nil), n.children...)
		c.nodes[i] = &cp
	}
	return c
}

func (m *refModel) get(id ID) (*refNode, bool) {
	if id < 0 || int(id) >= len(m.nodes) {
		return nil, false
	}
	n := m.nodes[id]
	return n, n.alive
}

func (m *refModel) allocID() ID { return ID(len(m.nodes)) }

func (m *refModel) chain(d ID) []ID {
	var out []ID
	for {
		out = append(out, d)
		if d == 0 {
			return out
		}
		d = m.nodes[d].parent
	}
}

func (m *refModel) subBytes(d ID) int64 {
	var sum int64
	var walk func(ID)
	walk = func(id ID) {
		n := m.nodes[id]
		if n.isDir {
			for _, c := range n.children {
				walk(c)
			}
		} else {
			sum += n.size
		}
	}
	walk(d)
	return sum
}

func (m *refModel) subEntries(d ID) int64 {
	var sum int64
	var walk func(ID)
	walk = func(id ID) {
		for _, c := range m.nodes[id].children {
			sum++
			if m.nodes[c].isDir {
				walk(c)
			}
		}
	}
	walk(d)
	return sum
}

func (m *refModel) subReserve(d ID) int64 {
	var sum int64
	var walk func(ID)
	walk = func(id ID) {
		n := m.nodes[id]
		sum += n.reserve
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(d)
	return sum
}

func refRe(target error) *refErr { return &refErr{cat: target.Error(), dir: -1} }

func (m *refModel) check(chain []ID, addB, addE int64) *refErr {
	for _, d := range chain {
		n := m.nodes[d]
		eff := m.subBytes(d) + m.subReserve(d) + addB
		if n.bytesQuota >= 0 && eff > n.bytesQuota {
			return &refErr{cat: ErrBytesQuota.Error(), dir: d}
		}
		en := m.subEntries(d) + addE
		if n.entriesQuota >= 0 && en > n.entriesQuota {
			return &refErr{cat: ErrEntriesQuota.Error(), dir: d}
		}
	}
	return nil
}

func (m *refModel) dirOf(id ID) (*refNode, *refErr) {
	n, ok := m.get(id)
	if !ok {
		return nil, refRe(ErrNotFound)
	}
	if !n.isDir {
		return nil, refRe(ErrWrongType)
	}
	return n, nil
}

// apply runs one op against the naive model mutating it in place.
func (m *refModel) apply(op Op) (ID, *refErr) {
	switch op.Kind {
	case OpMkdir:
		p, e := m.dirOf(op.P)
		if e != nil {
			return 0, e
		}
		if e := m.check(m.chain(op.P), 0, 1); e != nil {
			return 0, e
		}
		id := m.allocID()
		m.nodes = append(m.nodes, &refNode{
			alive: true, isDir: true, parent: op.P,
			bytesQuota: -1, entriesQuota: -1, children: []ID{},
		})
		p.children = append(p.children, id)
		return id, nil
	case OpAddFile:
		if op.Size < 0 {
			return 0, refRe(ErrInvalid)
		}
		p, e := m.dirOf(op.P)
		if e != nil {
			return 0, e
		}
		c := op.Size
		if c > p.reserve {
			c = p.reserve
		}
		if e := m.check(m.chain(op.P), op.Size-c, 1); e != nil {
			return 0, e
		}
		id := m.allocID()
		m.nodes = append(m.nodes, &refNode{alive: true, parent: op.P, size: op.Size})
		p.children = append(p.children, id)
		p.reserve -= c
		return id, nil
	case OpSetQuota:
		if op.Bytes < -1 || op.N < -1 {
			return 0, refRe(ErrInvalid)
		}
		n, e := m.dirOf(op.X)
		if e != nil {
			return 0, e
		}
		if op.Bytes >= 0 && m.subBytes(op.X)+m.subReserve(op.X) > op.Bytes {
			return 0, &refErr{cat: ErrBelowUsage.Error(), dir: op.X}
		}
		if op.N >= 0 && m.subEntries(op.X) > op.N {
			return 0, &refErr{cat: ErrBelowUsage.Error(), dir: op.X}
		}
		n.bytesQuota = op.Bytes
		n.entriesQuota = op.N
		return 0, nil
	case OpReserve:
		if op.Bytes <= 0 {
			return 0, refRe(ErrInvalid)
		}
		n, e := m.dirOf(op.X)
		if e != nil {
			return 0, e
		}
		if e := m.check(m.chain(op.X), op.Bytes, 0); e != nil {
			return 0, e
		}
		n.reserve += op.Bytes
		return 0, nil
	case OpRelease:
		if op.Bytes <= 0 {
			return 0, refRe(ErrInvalid)
		}
		n, e := m.dirOf(op.X)
		if e != nil {
			return 0, e
		}
		if op.Bytes > n.reserve {
			return 0, refRe(ErrInsuffReserve)
		}
		n.reserve -= op.Bytes
		return 0, nil
	case OpResize:
		if op.Size < 0 {
			return 0, refRe(ErrInvalid)
		}
		n, ok := m.get(op.X)
		if !ok {
			return 0, refRe(ErrNotFound)
		}
		if n.isDir {
			return 0, refRe(ErrWrongType)
		}
		if delta := op.Size - n.size; delta > 0 {
			if e := m.check(m.chain(n.parent), delta, 0); e != nil {
				return 0, e
			}
		}
		n.size = op.Size
		return 0, nil
	case OpRemove:
		if op.X == 0 {
			return 0, refRe(ErrRoot)
		}
		n, ok := m.get(op.X)
		if !ok {
			return 0, refRe(ErrNotFound)
		}
		if n.isDir && len(n.children) > 0 {
			return 0, refRe(ErrNotEmpty)
		}
		p := m.nodes[n.parent]
		for i, c := range p.children {
			if c == op.X {
				p.children = append(p.children[:i], p.children[i+1:]...)
				break
			}
		}
		n.alive = false
		return 0, nil
	case OpRename:
		if op.X == 0 {
			return 0, refRe(ErrRoot)
		}
		xn, ok := m.get(op.X)
		if !ok {
			return 0, refRe(ErrNotFound)
		}
		pn, e := m.dirOf(op.P)
		if e != nil {
			return 0, e
		}
		if op.P == xn.parent {
			return 0, nil
		}
		for cur := op.P; ; {
			if cur == op.X {
				return 0, refRe(ErrBadStructure)
			}
			if cur == 0 {
				break
			}
			cur = m.nodes[cur].parent
		}
		var payB, payE int64
		if xn.isDir {
			payB = m.subBytes(op.X) + m.subReserve(op.X)
			payE = m.subEntries(op.X) + 1
		} else {
			payB = xn.size
			payE = 1
		}
		old := map[ID]bool{}
		for _, c := range m.chain(xn.parent) {
			old[c] = true
		}
		var newOnly []ID
		for _, c := range m.chain(op.P) {
			if !old[c] {
				newOnly = append(newOnly, c)
			}
		}
		if e := m.check(newOnly, payB, payE); e != nil {
			return 0, e
		}
		oldp := m.nodes[xn.parent]
		for i, c := range oldp.children {
			if c == op.X {
				oldp.children = append(oldp.children[:i], oldp.children[i+1:]...)
				break
			}
		}
		pn.children = append(pn.children, op.X)
		xn.parent = op.P
		return 0, nil
	default:
		return 0, refRe(ErrInvalid)
	}
}

func classify(err error) refErr {
	if err == nil {
		return refErr{}
	}
	for _, tg := range []error{
		ErrInvalid, ErrNotFound, ErrWrongType, ErrRoot, ErrNotEmpty,
		ErrBadStructure, ErrInsuffReserve, ErrBytesQuota, ErrEntriesQuota,
		ErrBelowUsage,
	} {
		if errors.Is(err, tg) {
			r := refErr{cat: tg.Error(), dir: -1}
			var qe *QuotaError
			if errors.As(err, &qe) {
				r.dir = qe.Dir
			}
			return r
		}
	}
	return refErr{cat: err.Error(), dir: -1}
}

func dirList(m *refModel) []ID {
	var out []ID
	for i, n := range m.nodes {
		if n.alive && n.isDir {
			out = append(out, ID(i))
		}
	}
	return out
}

func aliveList(m *refModel) []ID {
	var out []ID
	for i, n := range m.nodes {
		if n.alive {
			out = append(out, ID(i))
		}
	}
	return out
}

func pickID(rng *rand.Rand, xs []ID) ID { return xs[rng.Intn(len(xs))] }

func randLimit(rng *rand.Rand, span int64) int64 {
	if rng.Intn(3) == 0 {
		return -1
	}
	return rng.Int63n(span)
}

// genOp picks a random op. pending are ids that earlier ops in the same batch
// will allocate; refTarget lets the generator aim at stale (removed/never) ids.
func genOp(rng *rand.Rand, m *refModel, pending []ID) Op {
	dirs := append(append([]ID{}, dirList(m)...), pending...)
	alive := aliveList(m)
	if len(dirs) == 0 {
		dirs = []ID{0}
	}
	pick := func(xs []ID) ID {
		id := pickID(rng, xs)
		if rng.Intn(8) == 0 {
			id = ID(len(m.nodes) + rng.Intn(4)) // likely-stale id
		}
		return id
	}
	switch rng.Intn(10) {
	case 0, 1:
		return Op{Kind: OpMkdir, P: pickID(rng, dirs)}
	case 2, 3:
		size := rng.Int63n(12)
		if rng.Intn(10) == 0 {
			size = -1
		}
		return Op{Kind: OpAddFile, P: pickID(rng, dirs), Size: size}
	case 4:
		return Op{Kind: OpSetQuota, X: pickID(rng, dirs),
			Bytes: randLimit(rng, 60), N: randLimit(rng, 8)}
	case 5:
		return Op{Kind: OpReserve, X: pickID(rng, dirs), Bytes: int64(1 + rng.Intn(15))}
	case 6:
		return Op{Kind: OpRelease, X: pickID(rng, dirs), Bytes: int64(1 + rng.Intn(15))}
	case 7:
		var x ID
		if len(alive) > 0 {
			x = pick(alive)
		} else {
			x = pickID(rng, dirs)
		}
		size := rng.Int63n(20)
		if rng.Intn(10) == 0 {
			size = -1
		}
		return Op{Kind: OpResize, X: x, Size: size}
	case 8:
		x := ID(0)
		if len(alive) > 0 {
			x = pick(alive)
		}
		return Op{Kind: OpRemove, X: x}
	default:
		x := ID(1)
		if len(alive) > 0 {
			x = pick(alive)
		}
		return Op{Kind: OpRename, X: x, P: pickID(rng, dirs)}
	}
}

func opString(op Op) string {
	switch op.Kind {
	case OpMkdir:
		return "Mkdir(" + itoa(int64(op.P)) + ")"
	case OpAddFile:
		return "AddFile(" + itoa(int64(op.P)) + "," + itoa(op.Size) + ")"
	case OpResize:
		return "Resize(" + itoa(int64(op.X)) + "," + itoa(op.Size) + ")"
	case OpRemove:
		return "Remove(" + itoa(int64(op.X)) + ")"
	case OpRename:
		return "Rename(" + itoa(int64(op.X)) + "->" + itoa(int64(op.P)) + ")"
	case OpReserve:
		return "Reserve(" + itoa(int64(op.X)) + "," + itoa(op.Bytes) + ")"
	case OpRelease:
		return "Release(" + itoa(int64(op.X)) + "," + itoa(op.Bytes) + ")"
	case OpSetQuota:
		return "SetQuota(" + itoa(int64(op.X)) + "," + itoa(op.Bytes) + "," + itoa(op.N) + ")"
	default:
		return op.Kind
	}
}

func (m *refModel) fullUsage() map[ID][3]int64 {
	out := map[ID][3]int64{}
	for i, n := range m.nodes {
		if !n.alive || !n.isDir {
			continue
		}
		d := ID(i)
		out[d] = [3]int64{m.subBytes(d), m.subEntries(d), m.subReserve(d)}
	}
	return out
}

// runOne executes op on both implementations and compares the outcome and
// the entire recomputed tree usage.
func runOne(t *testing.T, l *Ledger, ref *refModel, op Op, tag string) {
	t.Helper()

	var gotID ID
	var gotErr error
	switch op.Kind {
	case OpMkdir:
		gotID, gotErr = l.Mkdir(op.P)
	case OpAddFile:
		gotID, gotErr = l.AddFile(op.P, op.Size)
	case OpResize:
		gotErr = l.Resize(op.X, op.Size)
	case OpRemove:
		gotErr = l.Remove(op.X)
	case OpRename:
		gotErr = l.Rename(op.X, op.P)
	case OpReserve:
		gotErr = l.Reserve(op.X, op.Bytes)
	case OpRelease:
		gotErr = l.Release(op.X, op.Bytes)
	case OpSetQuota:
		gotErr = l.SetQuota(op.X, op.Bytes, op.N)
	}

	wantID, wantErr := ref.apply(op)
	got := classify(gotErr)

	t.Logf("%s input=%s output=(id=%d, err=%s) basis=(want id=%d, want=%s)",
		tag, opString(op), gotID, errLabel(got), wantID, errLabelDeref(wantErr))

	if gotErr == nil && gotID != wantID {
		t.Fatalf("%s id mismatch: got %d want %d for %s", tag, gotID, wantID, opString(op))
	}
	if (gotErr == nil) != (wantErr == nil) || (wantErr != nil && got != *wantErr) {
		t.Fatalf("%s result mismatch for %s:\n got %v\nwant %v", tag, opString(op), got, wantErr)
	}

	wantUsage := ref.fullUsage()
	for d, w := range wantUsage {
		b, e, r, err := l.Usage(d)
		if err != nil {
			t.Fatalf("%s Usage(%d): %v", tag, d, err)
		}
		if b != w[0] || e != w[1] || r != w[2] {
			t.Fatalf("%s Usage(%d)=(%d,%d,%d) want (%d,%d,%d) after %s",
				tag, d, b, e, r, w[0], w[1], w[2], opString(op))
		}
		// Effective bytes must never exceed quota; same for entries.
		n := ref.nodes[d]
		if n.bytesQuota >= 0 && b+r > n.bytesQuota {
			t.Fatalf("%s E(%d)=%d > quota %d", tag, d, b+r, n.bytesQuota)
		}
		if n.entriesQuota >= 0 && e > n.entriesQuota {
			t.Fatalf("%s entries(%d)=%d > quota %d", tag, d, e, n.entriesQuota)
		}
	}
}

func errLabel(r refErr) string {
	if r.cat == "" {
		return "ok"
	}
	return r.cat + "@" + itoa(int64(r.dir))
}

func errLabelDeref(r *refErr) string {
	if r == nil {
		return "ok"
	}
	return errLabel(*r)
}

func TestDifferentialRandom(t *testing.T) {
	const sequences = 2000
	const seqLen = 40
	for s := 0; s < sequences; s++ {
		rng := rand.New(rand.NewSource(int64(s) + 1))
		l := New()
		ref := newRef()
		tag := "seq" + itoa(int64(s))

		for step := 0; step < seqLen; step++ {
			if rng.Intn(3) == 0 {
				// Build a random batch, letting later ops reference ids that
				// earlier ops in the same batch will allocate.
				k := 1 + rng.Intn(5)
				ops := make([]Op, 0, k)
				sim := ref.clone()
				pending := []ID{}
				nextID := sim.allocID()
				for j := 0; j < k; j++ {
					op := genOp(rng, sim, pending)
					ops = append(ops, op)
					_, e := sim.apply(op)
					if e != nil {
						break
					}
					if op.Kind == OpMkdir || op.Kind == OpAddFile {
						pending = append(pending, nextID)
						nextID++
					}
				}

				gotIDs, gotErr := l.Batch(ops)

				// Reference batch: replay on a clone; commit only if all pass.
				work := ref.clone()
				var wantIDs []ID
				var failAt = -1
				var failRef *refErr
				for j, op := range ops {
					id, e := work.apply(op)
					if e != nil {
						failAt, failRef = j, e
						break
					}
					if op.Kind == OpMkdir || op.Kind == OpAddFile {
						wantIDs = append(wantIDs, id)
					}
				}

				log := tag + " Batch["
				for j, op := range ops {
					if j > 0 {
						log += "; "
					}
					log += opString(op)
				}
				log += "]"

				if failAt >= 0 {
					var be *BatchError
					if !errors.As(gotErr, &be) || be.Index != failAt {
						t.Fatalf("%s batch failure index: got %v want %d\n%s", tag, gotErr, failAt, log)
					}
					if !errors.Is(gotErr, ErrBatch) {
						t.Fatalf("%s batch error must satisfy ErrBatch: %v", tag, gotErr)
					}
					got := classify(gotErr)
					if got != *failRef {
						t.Fatalf("%s batch failure reason: got %s want %s\n%s",
							tag, errLabel(got), errLabel(*failRef), log)
					}
					t.Logf("%s output=fail@%d(%s) basis=rolled back, state unchanged",
						log, failAt, errLabel(*failRef))
					// ref itself was not mutated; compare usage anyway.
				} else {
					if gotErr != nil {
						t.Fatalf("%s unexpected batch failure: %v\n%s", tag, gotErr, log)
					}
					if len(gotIDs) != len(wantIDs) {
						t.Fatalf("%s batch ids: got %v want %v", tag, gotIDs, wantIDs)
					}
					for j := range wantIDs {
						if gotIDs[j] != wantIDs[j] {
							t.Fatalf("%s batch ids: got %v want %v", tag, gotIDs, wantIDs)
						}
					}
					ref = work
					t.Logf("%s output=ok ids=%v basis=committed", log, gotIDs)
				}

				// Compare the whole tree after every batch (committed or
				// rolled back).
				wantUsage := ref.fullUsage()
				for d, w := range wantUsage {
					b, e, r, err := l.Usage(d)
					if err != nil {
						t.Fatalf("%s Usage(%d): %v", tag, d, err)
					}
					if b != w[0] || e != w[1] || r != w[2] {
						t.Fatalf("%s post-batch Usage(%d)=(%d,%d,%d) want (%d,%d,%d)\n%s",
							tag, d, b, e, r, w[0], w[1], w[2], log)
					}
				}
			} else {
				op := genOp(rng, ref, nil)
				runOne(t, l, ref, op, tag)
			}
		}
	}
}
