package pagetable

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// This file holds an independent naive model of the mapper, written directly
// from the specification rules: the state is a sorted list of leaves, and the
// table count is derived from the leaf set. The differential test replays
// random operation sequences against both implementations and compares every
// return value, the leaf set, and the counters, logging inputs, outputs and
// the basis of each judgment.

type mLeaf struct {
	start, size, pfn int
	w, a, d          bool
}

// model is the naive reference implementation.
type model struct {
	quota  int
	leaves []mLeaf // sorted by start, pairwise non-overlapping
	epoch  int
}

func (md *model) tables() int {
	l1 := map[int]bool{}
	l0 := map[int]bool{}
	for _, lf := range md.leaves {
		if lf.size < 64 {
			l1[lf.start/64] = true
		}
		if lf.size == 1 {
			l0[lf.start/8] = true
		}
	}
	return len(l1) + len(l0)
}

func (md *model) mapped() int {
	sum := 0
	for _, lf := range md.leaves {
		sum += lf.size
	}
	return sum
}

func (md *model) leafAt(vpn int) *mLeaf {
	for i := range md.leaves {
		if md.leaves[i].start <= vpn && vpn < md.leaves[i].start+md.leaves[i].size {
			return &md.leaves[i]
		}
	}
	return nil
}

func (md *model) insertLeaf(lf mLeaf) {
	i := 0
	for i < len(md.leaves) && md.leaves[i].start < lf.start {
		i++
	}
	md.leaves = append(md.leaves, mLeaf{})
	copy(md.leaves[i+1:], md.leaves[i:])
	md.leaves[i] = lf
}

func (md *model) mapOp(vpn, size, pfn int, w bool) ErrCode {
	if size != 1 && size != 8 && size != 64 {
		return ErrInvalidArgument
	}
	if vpn < 0 || vpn >= NumPages || pfn < 0 || pfn >= MaxPfn {
		return ErrInvalidArgument
	}
	if vpn%size != 0 || pfn%size != 0 {
		return ErrMisaligned
	}
	for _, lf := range md.leaves {
		if lf.start < vpn+size && vpn < lf.start+lf.size {
			return ErrOverlap
		}
	}
	need := 0
	if size < 64 {
		hasL1 := false
		for _, lf := range md.leaves {
			if lf.size < 64 && lf.start/64 == vpn/64 {
				hasL1 = true
				break
			}
		}
		if !hasL1 {
			need++
		}
	}
	if size == 1 {
		hasL0 := false
		for _, lf := range md.leaves {
			if lf.size == 1 && lf.start/8 == vpn/8 {
				hasL0 = true
				break
			}
		}
		if !hasL0 {
			need++
		}
	}
	if md.tables()+need > md.quota {
		return ErrQuotaExceeded
	}
	md.insertLeaf(mLeaf{start: vpn, size: size, pfn: pfn, w: w})
	md.epoch++
	return -1
}

// unmapLeafList removes [lo,hi) from a leaf list, splitting partially covered
// leaves (children inherit pfn+i*sub, W, A, D). It reports removed pages and
// the number of splits (each split creates one table page).
func unmapLeafList(leaves []mLeaf, lo, hi int) (out []mLeaf, removed, splits int) {
	for _, lf := range leaves {
		lfLo, lfHi := lf.start, lf.start+lf.size
		switch {
		case lfHi <= lo || hi <= lfLo:
			out = append(out, lf)
		case lo <= lfLo && lfHi <= hi:
			removed += lf.size
		default:
			splits++
			sub := lf.size / 8
			children := make([]mLeaf, 0, 8)
			for i := 0; i < 8; i++ {
				children = append(children, mLeaf{
					start: lfLo + i*sub, size: sub,
					pfn: lf.pfn + i*sub, w: lf.w, a: lf.a, d: lf.d,
				})
			}
			kept, r, s := unmapLeafList(children, lo, hi)
			removed += r
			splits += s
			out = append(out, kept...)
		}
	}
	return out, removed, splits
}

func (md *model) unmapOp(vpn, n int) (int, ErrCode) {
	if vpn < 0 || vpn >= NumPages || n < 1 || vpn+n > NumPages {
		return 0, ErrInvalidArgument
	}
	work := make([]mLeaf, len(md.leaves))
	copy(work, md.leaves)
	work, removed, splits := unmapLeafList(work, vpn, vpn+n)
	if splits > 0 && md.tables()+splits > md.quota {
		return 0, ErrQuotaExceeded
	}
	if removed > 0 {
		md.leaves = work
		md.epoch++
	}
	return removed, -1
}

func (md *model) touchOp(vpn int, write bool) ErrCode {
	if vpn < 0 || vpn >= NumPages {
		return ErrInvalidArgument
	}
	lf := md.leafAt(vpn)
	if lf == nil {
		return ErrUnmapped
	}
	if write && !lf.w {
		return ErrWriteProtected
	}
	lf.a = true
	if write {
		lf.d = true
	}
	return -1
}

func (md *model) scanDirtyOp() []Leaf {
	var out []Leaf
	for i := range md.leaves {
		lf := &md.leaves[i]
		if lf.d {
			out = append(out, Leaf{Start: lf.start, Size: lf.size, Pfn: lf.pfn, W: lf.w, A: lf.a, D: true})
			lf.d = false
		}
	}
	return out
}

func (md *model) promoteOp(vpn, size int) ErrCode {
	if size != 8 && size != 64 {
		return ErrInvalidArgument
	}
	if vpn < 0 || vpn >= NumPages {
		return ErrInvalidArgument
	}
	if vpn%size != 0 {
		return ErrMisaligned
	}
	base := vpn
	var inRange []*mLeaf
	for i := range md.leaves {
		lf := &md.leaves[i]
		if lf.start < base+size && base < lf.start+lf.size {
			inRange = append(inRange, lf)
		}
	}
	for _, lf := range inRange {
		if lf.start <= base && lf.size >= size {
			return ErrNotATable // slot empty-or-leaf: covered by same/larger leaf
		}
	}
	if len(inRange) == 0 {
		return ErrNotATable // slot empty
	}
	sub := size / 8
	subs := make([]*mLeaf, 8)
	for i := 0; i < 8; i++ {
		cs := base + i*sub
		for _, lf := range inRange {
			if lf.start == cs && lf.size == sub {
				subs[i] = lf
				break
			}
		}
		if subs[i] == nil {
			return ErrNotAllLeaves
		}
	}
	pfn0 := subs[0].pfn
	if pfn0%size != 0 {
		return ErrPfnMismatch
	}
	for i := 1; i < 8; i++ {
		if subs[i].pfn != pfn0+i*sub {
			return ErrPfnMismatch
		}
	}
	w := subs[0].w
	for i := 1; i < 8; i++ {
		if subs[i].w != w {
			return ErrWritableMismatch
		}
	}
	var a, d bool
	for i := 0; i < 8; i++ {
		a = a || subs[i].a
		d = d || subs[i].d
	}
	// Replace the 8 children with the merged leaf, keeping the list sorted.
	out := md.leaves[:0]
	for i := range md.leaves {
		lf := &md.leaves[i]
		if lf.start < base+size && base < lf.start+lf.size {
			continue
		}
		out = append(out, *lf)
	}
	md.leaves = out
	md.insertLeaf(mLeaf{start: base, size: size, pfn: pfn0, w: w, a: a, d: d})
	md.epoch++
	return -1
}

func (md *model) translateOp(vpn int) (Translation, bool) {
	if vpn < 0 || vpn >= NumPages {
		return Translation{}, false
	}
	lf := md.leafAt(vpn)
	if lf == nil {
		return Translation{}, false
	}
	return Translation{Pfn: lf.pfn + vpn - lf.start, Size: lf.size, W: lf.w, A: lf.a, D: lf.d}, true
}

func (md *model) leavesView() []Leaf {
	var out []Leaf
	for _, lf := range md.leaves {
		out = append(out, Leaf{Start: lf.start, Size: lf.size, Pfn: lf.pfn, W: lf.w, A: lf.a, D: lf.d})
	}
	return out
}

// errCodeOf extracts the ErrCode of an operation error, or -1 for nil.
func errCodeOf(t *testing.T, err error) ErrCode {
	t.Helper()
	if err == nil {
		return -1
	}
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("non-*Error returned: %T %v", err, err)
	}
	return pe.Code
}

func leavesEqual(a, b []Leaf) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// checkState compares the full observable state of m against the model.
func checkState(t *testing.T, seq, op int, m *Mapper, md *model) {
	t.Helper()
	if got, want := m.Leaves(), md.leavesView(); !leavesEqual(got, want) {
		t.Fatalf("seq=%d op=%d: Leaves()=%v, model=%v", seq, op, got, want)
	}
	if got, want := m.Tables(), md.tables(); got != want {
		t.Fatalf("seq=%d op=%d: Tables()=%d, model=%d", seq, op, got, want)
	}
	if got, want := m.Mapped(), md.mapped(); got != want {
		t.Fatalf("seq=%d op=%d: Mapped()=%d, model=%d", seq, op, got, want)
	}
	if got, want := m.Epoch(), md.epoch; got != want {
		t.Fatalf("seq=%d op=%d: Epoch()=%d, model=%d", seq, op, got, want)
	}
}

// checkTranslate sweeps all 512 translations and compares them with the model.
func checkTranslate(t *testing.T, seq, op int, m *Mapper, md *model) {
	t.Helper()
	for vpn := 0; vpn < NumPages; vpn++ {
		got, gotOK := m.Translate(vpn)
		want, wantOK := md.translateOp(vpn)
		if gotOK != wantOK || got != want {
			t.Fatalf("seq=%d op=%d: Translate(%d)=(%+v,%v), model=(%+v,%v)",
				seq, op, vpn, got, gotOK, want, wantOK)
		}
	}
}

// TestDifferentialRandom replays 2000 random operation sequences against the
// naive model, comparing return values, error codes, leaf sets and counters
// after every step, and logging inputs, outputs and the judgment basis.
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1150))
	const sequences = 2000
	stats := map[string]int{}
	for seq := 0; seq < sequences; seq++ {
		// Mix tight quotas (frequent quota rejections) with generous ones.
		quota := 1 + rng.Intn(72)
		if seq%7 == 0 {
			quota = 72
		}
		if seq%11 == 0 {
			quota = 1 + rng.Intn(4)
		}
		m, err := New(quota)
		if err != nil {
			t.Fatalf("seq=%d: New(%d): %v", seq, quota, err)
		}
		md := &model{quota: quota}
		ops := 10 + rng.Intn(40)
		label := func(c ErrCode) string {
			if c == -1 {
				return "ok"
			}
			return c.String()
		}
		for op := 0; op < ops; op++ {
			kind := rng.Intn(14)
			switch {
			case kind <= 3: // Map, weighted
				size := []int{1, 8, 64}[rng.Intn(3)]
				vpn := rng.Intn(NumPages + 1)
				pfn := rng.Intn(MaxPfn + 1)
				if rng.Intn(4) > 0 { // usually aligned
					vpn = (vpn &^ (size - 1)) % NumPages
					pfn &^= size - 1
				}
				w := rng.Intn(2) == 0
				got := errCodeOf(t, m.Map(vpn, size, pfn, w))
				want := md.mapOp(vpn, size, pfn, w)
				t.Logf("seq=%d op=%d in=Map(vpn=%d,size=%d,pfn=%d,w=%v) out=%v model=%v basis=error-code", seq, op, vpn, size, pfn, w, got, want)
				if got != want {
					t.Fatalf("seq=%d op=%d: Map(%d,%d,%d,%v) err=%v, model=%v", seq, op, vpn, size, pfn, w, got, want)
				}
				stats["map/"+label(got)]++
			case kind == 4:
				// Constructive cluster: try to build 8 contiguous 1-page
				// leaves with contiguous pfns and identical W, setting up
				// a future Promote.
				base := 8 * rng.Intn(64)
				pfn0 := 8 * rng.Intn(MaxPfn/8)
				w := rng.Intn(2) == 0
				for i := 0; i < 8; i++ {
					got := errCodeOf(t, m.Map(base+i, 1, pfn0+i, w))
					want := md.mapOp(base+i, 1, pfn0+i, w)
					t.Logf("seq=%d op=%d in=Map(vpn=%d,size=1,pfn=%d,w=%v) out=%v model=%v basis=error-code(cluster)", seq, op, base+i, pfn0+i, w, got, want)
					if got != want {
						t.Fatalf("seq=%d op=%d: cluster Map(%d,1,%d,%v) err=%v, model=%v", seq, op, base+i, pfn0+i, w, got, want)
					}
				}
				stats["cluster"]++
			case kind <= 5: // Unmap
				vpn := rng.Intn(NumPages + 1)
				n := rng.Intn(NumPages + 2 - minInt(vpn, NumPages))
				if rng.Intn(3) > 0 && vpn < NumPages {
					n = 1 + rng.Intn(NumPages-vpn)
				}
				gotN, gotErr := m.Unmap(vpn, n)
				got := errCodeOf(t, gotErr)
				wantN, want := md.unmapOp(vpn, n)
				t.Logf("seq=%d op=%d in=Unmap(vpn=%d,n=%d) out=(%d,%v) model=(%d,%v) basis=pages+error-code", seq, op, vpn, n, gotN, got, wantN, want)
				if got != want || gotN != wantN {
					t.Fatalf("seq=%d op=%d: Unmap(%d,%d)=(%d,%v), model=(%d,%v)", seq, op, vpn, n, gotN, got, wantN, want)
				}
				stats["unmap/"+label(got)]++
				stats["unmap-pages"] += gotN
			case kind <= 7: // Touch
				vpn := rng.Intn(NumPages + 1)
				write := rng.Intn(2) == 0
				got := errCodeOf(t, m.Touch(vpn, write))
				want := md.touchOp(vpn, write)
				t.Logf("seq=%d op=%d in=Touch(vpn=%d,write=%v) out=%v model=%v basis=error-code", seq, op, vpn, write, got, want)
				if got != want {
					t.Fatalf("seq=%d op=%d: Touch(%d,%v) err=%v, model=%v", seq, op, vpn, write, got, want)
				}
				stats["touch/"+label(got)]++
			case kind <= 8: // ScanDirty
				got := m.ScanDirty()
				want := md.scanDirtyOp()
				t.Logf("seq=%d op=%d in=ScanDirty() out=%v model=%v basis=dirty-leaf-list", seq, op, got, want)
				if !leavesEqual(got, want) {
					t.Fatalf("seq=%d op=%d: ScanDirty()=%v, model=%v", seq, op, got, want)
				}
				stats["scandirty"] += len(got)
			case kind <= 10: // Promote
				size := []int{8, 64}[rng.Intn(2)]
				vpn := rng.Intn(NumPages + 1)
				if rng.Intn(3) > 0 {
					vpn = (vpn &^ (size - 1)) % NumPages
				}
				got := errCodeOf(t, m.Promote(vpn, size))
				want := md.promoteOp(vpn, size)
				t.Logf("seq=%d op=%d in=Promote(vpn=%d,size=%d) out=%v model=%v basis=error-code", seq, op, vpn, size, got, want)
				if got != want {
					t.Fatalf("seq=%d op=%d: Promote(%d,%d) err=%v, model=%v", seq, op, vpn, size, got, want)
				}
				stats["promote/"+label(got)]++
			default: // Translate spot check
				vpn := rng.Intn(NumPages + 1)
				got, gotOK := m.Translate(vpn)
				want, wantOK := md.translateOp(vpn)
				t.Logf("seq=%d op=%d in=Translate(vpn=%d) out=(%+v,%v) model=(%+v,%v) basis=translation", seq, op, vpn, got, gotOK, want, wantOK)
				if gotOK != wantOK || got != want {
					t.Fatalf("seq=%d op=%d: Translate(%d)=(%+v,%v), model=(%+v,%v)", seq, op, vpn, got, gotOK, want, wantOK)
				}
			}
			checkState(t, seq, op, m, md)
		}
		checkTranslate(t, seq, ops, m, md)
		if seq%500 == 0 {
			t.Logf("seq=%d done: quota=%d ops=%d leaves=%d tables=%d epoch=%d basis=full-state-match",
				seq, quota, ops, len(md.leaves), md.tables(), md.epoch)
		}
	}
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("stats %s = %d", k, stats[k])
	}
	// The random sequences must actually exercise the interesting paths.
	for _, k := range []string{"map/ok", "unmap/ok", "promote/ok", "touch/ok"} {
		if stats[k] == 0 {
			t.Fatalf("random sequences never succeeded at %s", k)
		}
	}
	for _, k := range []string{
		"map/misaligned", "map/overlap", "map/table quota exceeded",
		"unmap/table quota exceeded", "touch/unmapped", "touch/write protected",
		"promote/misaligned", "promote/slot is not a table", "promote/sub-slots are not all leaves",
	} {
		if stats[k] == 0 {
			t.Fatalf("random sequences never hit %s", k)
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
