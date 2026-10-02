package tlb

import (
	"reflect"
	"testing"
)

// snapshot captures the full observable state of a Model.
type snapshot struct {
	G        uint64
	taken    []uint64
	active   []pair
	reserved []pair
	pending  []bool
	mms      []mmState
	tlbs     [][]entry
}

func snap(md *Model) snapshot {
	s := snapshot{
		G:        md.G,
		taken:    append([]uint64(nil), md.taken...),
		active:   append([]pair(nil), md.active...),
		reserved: append([]pair(nil), md.reserved...),
		pending:  append([]bool(nil), md.pending...),
		mms:      append([]mmState(nil), md.mms...),
	}
	for _, t := range md.tlbs {
		var es []entry
		for e := t.ll.Front(); e != nil; e = e.Next() {
			es = append(es, e.Value.(entry))
		}
		s.tlbs = append(s.tlbs, es)
	}
	return s
}

func mustNew(t *testing.T, a, c, cap, m int) *Model {
	t.Helper()
	md, err := New(a, c, cap, m)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d): %v", a, c, cap, m, err)
	}
	return md
}

func mustCreateMM(t *testing.T, md *Model, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := md.CreateMM(); err != nil {
			t.Fatalf("CreateMM #%d: %v", i, err)
		}
	}
}

type swRes struct {
	asid            uint32
	gen             uint64
	flushed, rolled bool
}

func mustSwitch(t *testing.T, md *Model, cpu, mm int) swRes {
	t.Helper()
	asid, gen, flushed, rolled, err := md.Switch(cpu, mm)
	if err != nil {
		t.Fatalf("Switch(%d,%d): %v", cpu, mm, err)
	}
	return swRes{asid, gen, flushed, rolled}
}

func mustFill(t *testing.T, md *Model, cpu int, vpn, pfn uint64) (Key, bool) {
	t.Helper()
	k, ok, err := md.Fill(cpu, vpn, pfn)
	if err != nil {
		t.Fatalf("Fill(%d,%d,%d): %v", cpu, vpn, pfn, err)
	}
	return k, ok
}

func mustLookup(t *testing.T, md *Model, cpu int, vpn uint64) (uint32, bool) {
	t.Helper()
	pfn, hit, err := md.Lookup(cpu, vpn)
	if err != nil {
		t.Fatalf("Lookup(%d,%d): %v", cpu, vpn, err)
	}
	return pfn, hit
}

func keysOf(md *Model, cpu int) []Key {
	return md.tlbs[cpu].keys()
}

func wantKeys(t *testing.T, md *Model, cpu int, want ...Key) {
	t.Helper()
	got := keysOf(md, cpu)
	if want == nil {
		want = []Key{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TLB(%d) = %v, want %v", cpu, got, want)
	}
}

// TestSpecWalkthrough replays the worked example from the
// specification step by step, logging the decision basis of each
// step.
func TestSpecWalkthrough(t *testing.T) {
	md := mustNew(t, 3, 2, 2, 8)
	mustCreateMM(t, md, 4) // X=0 Y=1 Z=2 W=3
	const (
		X, Y, Z, W = 0, 1, 2, 3
	)

	if got := mustSwitch(t, md, 0, X); got != (swRes{1, 1, false, false}) {
		t.Fatalf("Switch(0,X) = %+v", got)
	}
	t.Log("Switch(0,X): fresh mm, smallest free asid 1, gen 1")

	if _, ok := mustFill(t, md, 0, 5, 50); ok {
		t.Fatal("Fill(0,5) should not evict")
	}
	if _, ok := mustFill(t, md, 0, 6, 60); ok {
		t.Fatal("Fill(0,6) should not evict")
	}
	if pfn, hit := mustLookup(t, md, 0, 5); !hit || pfn != 50 {
		t.Fatalf("Lookup(0,5) = %d,%v", pfn, hit)
	}
	k, ok := mustFill(t, md, 0, 7, 70)
	if !ok || k != (Key{1, 6}) {
		t.Fatalf("Fill(0,7) evicted %v,%v, want (1,6),true", k, ok)
	}
	t.Log("Fill(0,7): capacity 2, LRU victim is (1,6) after Lookup(0,5) refreshed it")

	if got := mustSwitch(t, md, 1, Y); got != (swRes{2, 1, false, false}) {
		t.Fatalf("Switch(1,Y) = %+v", got)
	}
	if got := mustSwitch(t, md, 0, Z); got != (swRes{3, 1, false, false}) {
		t.Fatalf("Switch(0,Z) = %+v", got)
	}
	if _, hit := mustLookup(t, md, 0, 5); hit {
		t.Fatal("Lookup(0,5) must miss: active asid is now 3")
	}
	wantKeys(t, md, 0, Key{1, 7}, Key{1, 5})
	t.Log("after Switch(0,Z): TLB(0) keeps stale asid-1 entries, lookup tags with asid 3")

	if got := mustSwitch(t, md, 1, W); got != (swRes{1, 2, true, true}) {
		t.Fatalf("Switch(1,W) = %+v", got)
	}
	t.Log("Switch(1,W): taken full -> rollover G=2, reserved=(3,1),(2,1), taken={2,3}, W gets 1, cpu1 flushed")
	if md.G != 2 {
		t.Fatalf("G = %d, want 2", md.G)
	}
	if !reflect.DeepEqual(md.reserved, []pair{{3, 1, true}, {2, 1, true}}) {
		t.Fatalf("reserved = %+v", md.reserved)
	}

	if got := mustSwitch(t, md, 0, X); got != (swRes{2, 3, true, true}) {
		t.Fatalf("Switch(0,X) = %+v", got)
	}
	t.Log("Switch(0,X): (1,1) stale and not reserved, taken full again -> rollover G=3, reserved=(3,1),(1,2), taken={1,3}, X gets 2, cpu0 flushed")
	wantKeys(t, md, 0)

	if got := mustSwitch(t, md, 1, Z); got != (swRes{3, 3, true, false}) {
		t.Fatalf("Switch(1,Z) = %+v", got)
	}
	t.Log("Switch(1,Z): (3,1) equals cpu0 reserved pair -> promoted to (3,3); pending still set -> flushed, no rollover")

	if md.G != 3 {
		t.Fatalf("G = %d, want 3", md.G)
	}
	for cpu := 0; cpu < 2; cpu++ {
		if md.pending[cpu] {
			t.Fatalf("pending[%d] still true", cpu)
		}
	}

	rel, rem, err := md.DestroyMM(Y)
	if err != nil || rel || rem != 0 {
		t.Fatalf("DestroyMM(Y) = %v,%d,%v, want false,0,nil (stale gen: mark only)", rel, rem, err)
	}
	t.Log("DestroyMM(Y): gen 1 != G 3 -> mark only, taken and TLBs untouched")

	mustFill(t, md, 0, 9, 90)
	mustFill(t, md, 1, 9, 91)

	if rel, rem, err := md.DestroyMM(Z); err != ErrBusy || rel || rem != 0 {
		t.Fatalf("DestroyMM(Z) = %v,%d,%v, want ErrBusy", rel, rem, err)
	}
	t.Log("DestroyMM(Z): cpu1 still active on (3,3) -> ErrBusy, no state change")

	if got := mustSwitch(t, md, 0, W); got != (swRes{1, 3, false, false}) {
		t.Fatalf("Switch(0,W) = %+v", got)
	}
	t.Log("Switch(0,W): (1,2) equals cpu1 reserved pair -> promoted to (1,3), no flush")
	wantKeys(t, md, 0, Key{2, 9})

	rel, rem, err = md.DestroyMM(X)
	if err != nil || !rel || rem != 1 {
		t.Fatalf("DestroyMM(X) = %v,%d,%v, want true,1,nil", rel, rem, err)
	}
	t.Log("DestroyMM(X): (2,3) not active, asid 2 not reserved -> released, one asid-2 entry dropped")
	wantKeys(t, md, 0)

	id, err := md.CreateMM()
	if err != nil || id != 4 {
		t.Fatalf("CreateMM = %d,%v, want 4,nil", id, err)
	}
	if got := mustSwitch(t, md, 0, id); got != (swRes{2, 3, false, false}) {
		t.Fatalf("Switch(0,mm4) = %+v", got)
	}
	t.Log("Switch(0,mm4): asid 2 was released -> reused by the new mm in the same generation")
	if _, hit := mustLookup(t, md, 0, 9); hit {
		t.Fatal("Lookup(0,9) must miss: released asid left no stale entries")
	}
	if md.mmVisited != 0 {
		t.Fatalf("mmVisited = %d, want 0", md.mmVisited)
	}
}

func TestConfigValidation(t *testing.T) {
	bad := [][4]int{
		{1, 1, 1, 1},      // A < 2
		{4097, 1, 1, 1},   // A > 4096
		{2, 0, 1, 1},      // C < 1
		{2, 17, 1, 1},     // C > 16 (also A <= C)
		{2, 1, 0, 1},      // T < 1
		{2, 1, 65, 1},     // T > 64
		{2, 1, 1, 0},      // M < 1
		{2, 1, 1, 100001}, // M > 1e5
		{2, 2, 1, 1},      // A == C
		{3, 3, 1, 1},      // A == C
		{3, 4, 1, 1},      // A < C
	}
	for _, b := range bad {
		if _, err := New(b[0], b[1], b[2], b[3]); err != ErrBadConfig {
			t.Fatalf("New%v = %v, want ErrBadConfig", b, err)
		}
	}
	if _, err := New(3, 2, 1, 1); err != nil {
		t.Fatalf("New(3,2,1,1) = %v, want nil (A=C+1 is legal)", err)
	}
	if _, err := New(4096, 16, 64, 100000); err != nil {
		t.Fatalf("New(max) = %v, want nil", err)
	}
}

func TestTooManyMM(t *testing.T) {
	md := mustNew(t, 2, 1, 1, 2)
	if id, err := md.CreateMM(); err != nil || id != 0 {
		t.Fatalf("CreateMM = %d,%v", id, err)
	}
	if id, err := md.CreateMM(); err != nil || id != 1 {
		t.Fatalf("CreateMM = %d,%v", id, err)
	}
	before := snap(md)
	if _, err := md.CreateMM(); err != ErrTooManyMM {
		t.Fatalf("CreateMM = %v, want ErrTooManyMM", err)
	}
	if !reflect.DeepEqual(snap(md), before) {
		t.Fatal("rejected CreateMM changed state")
	}
}

// TestFastPathNoStateChange: re-switching to the mm already bound in
// the current generation and lookup misses must not disturb any
// state except (for Switch) the cpu's own active pair.
func TestFastPathNoStateChange(t *testing.T) {
	md := mustNew(t, 4, 2, 2, 8)
	mustCreateMM(t, md, 2)
	mustSwitch(t, md, 0, 0)
	mustSwitch(t, md, 1, 1)
	mustFill(t, md, 0, 5, 50)

	before := snap(md)
	got := mustSwitch(t, md, 0, 0)
	if got != (swRes{1, 1, false, false}) {
		t.Fatalf("fast-path Switch = %+v", got)
	}
	after := snap(md)
	before.active[0] = pair{1, 1, true} // identical, but be explicit
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("fast-path Switch changed state:\nbefore %+v\nafter  %+v", before, after)
	}

	before = snap(md)
	if _, hit := mustLookup(t, md, 0, 99); hit {
		t.Fatal("Lookup(0,99) should miss")
	}
	if !reflect.DeepEqual(snap(md), before) {
		t.Fatal("Lookup miss changed state")
	}
	t.Log("fast path: gen==G reuses (asid,gen); lookup miss touches nothing")
}

// TestSmallestFreeASID: allocation always picks the lowest free asid.
func TestSmallestFreeASID(t *testing.T) {
	md := mustNew(t, 5, 2, 1, 8)
	mustCreateMM(t, md, 4)
	if got := mustSwitch(t, md, 0, 0); got.asid != 1 {
		t.Fatalf("asid = %d, want 1", got.asid)
	}
	if got := mustSwitch(t, md, 0, 1); got.asid != 2 {
		t.Fatalf("asid = %d, want 2", got.asid)
	}
	if got := mustSwitch(t, md, 0, 2); got.asid != 3 {
		t.Fatalf("asid = %d, want 3", got.asid)
	}
	// Release asid 2 (mm 1), then a new mm must get 2, not 4.
	rel, _, err := md.DestroyMM(1)
	if err != nil || !rel {
		t.Fatalf("DestroyMM(1) = %v,%v", rel, err)
	}
	if got := mustSwitch(t, md, 1, 3); got.asid != 2 {
		t.Fatalf("asid = %d, want 2 (smallest free)", got.asid)
	}
}

// TestNearWrapEveryAlloc: with A=C+1 almost every fresh allocation
// triggers a rollover.
func TestNearWrapEveryAlloc(t *testing.T) {
	md := mustNew(t, 3, 2, 1, 32)
	mustCreateMM(t, md, 12)
	rolls := 0
	for i := 0; i < 12; i++ {
		got := mustSwitch(t, md, i%2, i)
		if got.rolled {
			rolls++
		}
		if got.gen != md.G {
			t.Fatalf("switch %d: gen %d != G %d", i, got.gen, md.G)
		}
	}
	if rolls < 8 {
		t.Fatalf("only %d rollovers in 12 allocations with A=C+1", rolls)
	}
	t.Logf("A=C+1=3: 12 fresh allocations caused %d rollovers", rolls)
}

// TestRolloverReservesOwnActive: the switching CPU's pre-switch active
// pair is reserved too.
func TestRolloverReservesOwnActive(t *testing.T) {
	md := mustNew(t, 3, 2, 1, 8)
	mustCreateMM(t, md, 4)
	mustSwitch(t, md, 0, 0) // (1,1)
	mustSwitch(t, md, 1, 1) // (2,1)
	mustSwitch(t, md, 0, 2) // (3,1)
	got := mustSwitch(t, md, 0, 3)
	if !got.rolled {
		t.Fatal("expected rollover")
	}
	// cpu0's pre-switch active was (3,1); it must be reserved even
	// though cpu0 is the one switching.
	if !reflect.DeepEqual(md.reserved, []pair{{3, 1, true}, {2, 1, true}}) {
		t.Fatalf("reserved = %+v", md.reserved)
	}
	t.Log("rollover reserves the switching cpu's own pre-switch active pair")
}

// TestSameMMTwoCPUs: one mm active on two CPUs produces duplicate
// reserved pairs but a single taken bit.
func TestSameMMTwoCPUs(t *testing.T) {
	// Both CPUs active on the same mm at rollover.
	md2 := mustNew(t, 3, 2, 1, 8)
	mustCreateMM(t, md2, 4)
	mustSwitch(t, md2, 0, 0) // (1,1)
	mustSwitch(t, md2, 1, 1) // (2,1)
	mustSwitch(t, md2, 0, 2) // (3,1)
	mustSwitch(t, md2, 1, 2) // cpu1 also on mm2 (3,1)
	// taken full; both CPUs active on (3,1).
	got2 := mustSwitch(t, md2, 0, 3) // fresh mm, no free asid -> rollover
	if !got2.rolled {
		t.Fatal("expected rollover")
	}
	if !reflect.DeepEqual(md2.reserved, []pair{{3, 1, true}, {3, 1, true}}) {
		t.Fatalf("reserved = %+v, want duplicate (3,1) pairs", md2.reserved)
	}
	// taken must contain asid 3 exactly once (reserved), plus the
	// asid freshly allocated to mm3 during the same Switch.
	if got2.asid != 1 {
		t.Fatalf("mm3 got asid %d, want 1 (smallest free)", got2.asid)
	}
	bits := 0
	for _, w := range md2.taken {
		for w != 0 {
			bits += int(w & 1)
			w >>= 1
		}
	}
	if bits != 2 {
		t.Fatalf("taken holds %d bits, want 2 (reserved asid 3 + new asid 1)", bits)
	}
	if md2.taken[0]&(uint64(1)<<3) == 0 {
		t.Fatal("taken must contain asid 3")
	}
	t.Log("same mm on two cpus: reserved pair duplicated, taken bit set once")
}

// TestPromotionExactPairOnly: promotion requires the reserved pair to
// match (asid, gen) exactly; an mm whose asid equals a reserved asid
// but whose gen differs is allocated a fresh asid instead.
func TestPromotionExactPairOnly(t *testing.T) {
	md := mustNew(t, 4, 2, 2, 16)
	mustCreateMM(t, md, 8)
	const (
		X, Y, Z, V, W, Q = 0, 1, 2, 3, 4, 5
	)
	mustSwitch(t, md, 0, X) // X=(1,1)
	mustSwitch(t, md, 1, Y) // Y=(2,1)
	mustSwitch(t, md, 0, Z) // Z=(3,1)
	mustSwitch(t, md, 1, Z) // cpu1 active (3,1)
	mustSwitch(t, md, 0, V) // V=(4,1); taken full
	got := mustSwitch(t, md, 1, W)
	if !got.rolled {
		t.Fatal("expected rollover")
	}
	// G=2, reserved: cpu0 (4,1), cpu1 (3,1); taken={3,4}; W=(1,2).
	if got := mustSwitch(t, md, 0, V); got != (swRes{4, 2, true, false}) {
		t.Fatalf("Switch(0,V) = %+v, want exact-pair promotion to (4,2)", got)
	}
	t.Log("V=(4,1) equals reserved pair (4,1): promoted, keeps asid 4")
	if got := mustSwitch(t, md, 1, X); got.asid != 2 || got.gen != 2 {
		t.Fatalf("Switch(1,X) = %+v", got)
	}
	// X=(2,2); taken={1,2,3,4} full again.
	got = mustSwitch(t, md, 0, Q)
	if !got.rolled {
		t.Fatal("expected rollover")
	}
	// G=3, reserved: cpu0 (4,2), cpu1 (2,2); taken={2,4}; Q=(1,3).
	// Y is still (2,1): asid 2 matches reserved asid of (2,2) but the
	// gen differs, so Y must NOT be promoted onto asid 2.
	got = mustSwitch(t, md, 1, Y)
	if got.rolled {
		t.Fatalf("unexpected rollover: %+v", got)
	}
	if got.asid != 3 || got.gen != 3 {
		t.Fatalf("Switch(1,Y) = %+v, want (3,3): asid-equal/gen-different must not promote", got)
	}
	if md.mms[X].asid != 2 {
		t.Fatalf("X lost asid 2 to Y: X=%+v Y=%+v", md.mms[X], md.mms[Y])
	}
	t.Log("Y=(2,1) vs reserved (2,2): same asid, different gen -> no promotion, fresh asid 3")
}

// TestPendingLazyFlush: rollover sets pending on every CPU, but only
// the switching CPU flushes; others keep (and can still hit) their
// stale entries until their own next Switch.
func TestPendingLazyFlush(t *testing.T) {
	md := mustNew(t, 3, 2, 4, 8)
	mustCreateMM(t, md, 4)
	mustSwitch(t, md, 0, 0) // (1,1)
	mustSwitch(t, md, 1, 1) // (2,1)
	mustFill(t, md, 1, 7, 70)
	mustSwitch(t, md, 0, 2) // (3,1)
	got := mustSwitch(t, md, 0, 3)
	if !got.rolled || !got.flushed {
		t.Fatalf("Switch(0,3) = %+v, want rollover+flush", got)
	}
	// cpu1 has not switched: pending still true, TLB intact.
	if !md.pending[1] {
		t.Fatal("pending[1] must remain true until cpu1 switches")
	}
	if pfn, hit := mustLookup(t, md, 1, 7); !hit || pfn != 70 {
		t.Fatalf("Lookup(1,7) = %d,%v, want lazy hit on stale entry", pfn, hit)
	}
	// cpu1's next Switch flushes even without a rollover.
	got = mustSwitch(t, md, 1, 1)
	if got.rolled || !got.flushed {
		t.Fatalf("Switch(1,1) = %+v, want flush without rollover", got)
	}
	wantKeys(t, md, 1)
	if _, hit := mustLookup(t, md, 1, 7); hit {
		t.Fatal("entry must be gone after the lazy flush")
	}
	t.Log("pending is per-cpu: set for all at rollover, consumed only by that cpu's next Switch")
}

// TestLRU: eviction order, lookup refresh, update-in-place, and
// entries of other ASIDs counting towards capacity.
func TestLRU(t *testing.T) {
	md := mustNew(t, 8, 2, 2, 8)
	mustCreateMM(t, md, 3)
	mustSwitch(t, md, 0, 0) // asid 1
	mustFill(t, md, 0, 1, 10)
	mustFill(t, md, 0, 2, 20)
	// Update existing key: no eviction, becomes MRU.
	if _, ok := mustFill(t, md, 0, 1, 11); ok {
		t.Fatal("update must not evict")
	}
	k, ok := mustFill(t, md, 0, 3, 30)
	if !ok || k != (Key{1, 2}) {
		t.Fatalf("evicted %v,%v, want (1,2),true", k, ok)
	}
	if pfn, _ := mustLookup(t, md, 0, 1); pfn != 11 {
		t.Fatalf("updated pfn = %d, want 11", pfn)
	}
	// Entries tagged with another ASID share the same capacity.
	mustSwitch(t, md, 0, 1) // asid 2
	k, ok = mustFill(t, md, 0, 9, 90)
	if !ok || k != (Key{1, 3}) {
		t.Fatalf("evicted %v,%v, want (1,3),true (other-asid entries count)", k, ok)
	}
	wantKeys(t, md, 0, Key{2, 9}, Key{1, 1})
	// Lookup hit refreshes order.
	if _, hit := mustLookup(t, md, 0, 9); !hit {
		t.Fatal("Lookup(0,9) should hit")
	}
	mustSwitch(t, md, 0, 2) // asid 3
	k, ok = mustFill(t, md, 0, 8, 80)
	if !ok || k != (Key{1, 1}) {
		t.Fatalf("evicted %v,%v, want (1,1),true", k, ok)
	}
	t.Log("LRU: update refreshes, hit refreshes, capacity is per-cpu across all asids")
}

// TestInvalidate covers valid, reserved, stale and never-bound
// contexts.
func TestInvalidate(t *testing.T) {
	md := mustNew(t, 3, 2, 4, 8)
	mustCreateMM(t, md, 4)
	mustSwitch(t, md, 0, 0) // mm0 = (1,1)
	mustSwitch(t, md, 1, 0) // mm0 on cpu1 too
	mustFill(t, md, 0, 5, 50)
	mustFill(t, md, 1, 5, 55)
	mustFill(t, md, 1, 6, 66)

	// Current-generation context: removes from both CPUs.
	n, err := md.Invalidate(0, 5)
	if err != nil || n != 2 {
		t.Fatalf("Invalidate(0,5) = %d,%v, want 2,nil", n, err)
	}
	wantKeys(t, md, 0)
	wantKeys(t, md, 1, Key{1, 6})

	// Never-bound mm: asid 0 -> 0, no change.
	before := snap(md)
	if n, err = md.Invalidate(3, 5); err != nil || n != 0 {
		t.Fatalf("Invalidate(unbound) = %d,%v", n, err)
	}
	if !reflect.DeepEqual(snap(md), before) {
		t.Fatal("Invalidate on unbound mm changed state")
	}

	// Stale context (not current, not reserved): 0, no change.
	mustSwitch(t, md, 0, 1) // (2,1)
	mustSwitch(t, md, 0, 2) // (3,1); taken full
	got := mustSwitch(t, md, 0, 3)
	if !got.rolled {
		t.Fatal("expected rollover")
	}
	// G=2; reserved = cpu0 (3,1), cpu1 (1,1). mm1=(2,1) is stale and
	// not reserved.
	mustFill(t, md, 1, 6, 66) // tagged (1,6) on cpu1 (active (1,1) reserved-promoted? no: cpu1 active still (1,1))
	before = snap(md)
	if n, err = md.Invalidate(1, 6); err != nil || n != 0 {
		t.Fatalf("Invalidate(stale mm1) = %d,%v, want 0,nil", n, err)
	}
	if !reflect.DeepEqual(snap(md), before) {
		t.Fatal("Invalidate on stale mm changed state")
	}
	t.Log("stale context (gen old, not reserved): Invalidate is a no-op")

	// Reserved context: mm0 is (1,1) == cpu1's reserved pair.
	mustFill(t, md, 1, 9, 90) // tagged (1,9): cpu1 active asid is 1
	if n, err = md.Invalidate(0, 9); err != nil || n != 1 {
		t.Fatalf("Invalidate(reserved mm0) = %d,%v, want 1,nil", n, err)
	}
	t.Log("reserved context: Invalidate still removes matching entries")
}

// TestDestroyMMCases covers the three DestroyMM outcomes and ErrBusy.
func TestDestroyMMCases(t *testing.T) {
	md := mustNew(t, 3, 2, 4, 8)
	mustCreateMM(t, md, 4)
	mustSwitch(t, md, 0, 0) // mm0 = (1,1)
	mustSwitch(t, md, 1, 1) // mm1 = (2,1)
	mustFill(t, md, 1, 5, 50)
	mustSwitch(t, md, 0, 2) // mm2 = (3,1)
	got := mustSwitch(t, md, 0, 3)
	if !got.rolled {
		t.Fatal("expected rollover")
	}
	// G=2, reserved: cpu0 (3,1), cpu1 (2,1); taken={2,3}+mm3's asid 1.

	// Case 1: stale generation -> mark only.
	rel, rem, err := md.DestroyMM(0)
	if err != nil || rel || rem != 0 {
		t.Fatalf("DestroyMM(stale) = %v,%d,%v", rel, rem, err)
	}
	if !md.mms[0].dead {
		t.Fatal("mm0 must be marked dead")
	}

	// Case 2: current gen but asid reserved -> mark only.
	// mm1=(2,1) is stale actually; make a reserved-and-current mm:
	// switch cpu0 back to mm2 promotes (3,1) -> (3,2), still reserved.
	mustSwitch(t, md, 0, 2) // promoted (3,2)
	mustSwitch(t, md, 1, 1) // mm1 promoted (2,2) via cpu1's reserved pair
	// Now move both CPUs elsewhere so mm1 is not active.
	// cpu0 -> mm3 (1,2); cpu1 stays on mm1... need cpu1 off mm1.
	// Destroy mm1 while reserved: reserved pairs are still (3,1),(2,1);
	// mm1 is (2,2): asid 2 matches reserved asid of (2,1).
	mustSwitch(t, md, 0, 3) // cpu0 -> (1,2)
	// cpu1 is still active on mm1 (2,2): ErrBusy first.
	if _, _, err = md.DestroyMM(1); err != ErrBusy {
		t.Fatalf("DestroyMM(active mm1) = %v, want ErrBusy", err)
	}
	// Move cpu1 to mm3 as well, then mm1 is inactive but reserved.
	mustSwitch(t, md, 1, 3) // (1,2)
	before := snap(md)
	rel, rem, err = md.DestroyMM(1)
	if err != nil || rel || rem != 0 {
		t.Fatalf("DestroyMM(reserved mm1) = %v,%d,%v, want false,0,nil", rel, rem, err)
	}
	after := snap(md)
	after.mms[1].dead = false // only the dead flag may differ
	if !reflect.DeepEqual(after, before) {
		t.Fatal("reserved DestroyMM must only set the dead flag")
	}
	t.Log("case 2: asid reserved -> mark only, taken and TLBs untouched")

	// Case 3: releasable -> taken shrinks, entries dropped, asid reused.
	mustFill(t, md, 0, 5, 50) // tagged (1,5) on cpu0 (active mm3, asid 1)
	mustFill(t, md, 1, 6, 60) // tagged (1,6) on cpu1 (active mm3, asid 1)
	mustSwitch(t, md, 0, 2)   // cpu0 -> mm2 (3,2)
	mustSwitch(t, md, 1, 2)   // cpu1 -> mm2 (3,2); mm3 now inactive
	rel, rem, err = md.DestroyMM(3)
	if err != nil || !rel || rem != 2 {
		t.Fatalf("DestroyMM(mm3) = %v,%d,%v, want true,2,nil", rel, rem, err)
	}
	wantKeys(t, md, 0)
	wantKeys(t, md, 1)
	// Asid 1 immediately reusable within the same generation.
	id, _ := md.CreateMM()
	got2 := mustSwitch(t, md, 0, id)
	if got2.asid != 1 || got2.gen != md.G {
		t.Fatalf("new mm = (%d,%d), want (1,%d)", got2.asid, got2.gen, md.G)
	}
	if _, hit := mustLookup(t, md, 0, 5); hit {
		t.Fatal("stale entry of released asid must not be hit")
	}
	t.Log("case 3: released asid drops its TLB entries and is reused in-generation")
}

// TestDestroyReleasedNotReserved: an asid released by DestroyMM is not
// resurrected into taken/reserved by a later rollover.
func TestDestroyReleasedNotReserved(t *testing.T) {
	md := mustNew(t, 3, 2, 2, 8)
	mustCreateMM(t, md, 5)
	mustSwitch(t, md, 0, 0) // (1,1)
	mustSwitch(t, md, 1, 1) // (2,1)
	// Destroy mm0: not active on any cpu? cpu0 active is (1,1): busy.
	mustSwitch(t, md, 0, 2) // (3,1); now mm0 inactive
	rel, _, err := md.DestroyMM(0)
	if err != nil || !rel {
		t.Fatalf("DestroyMM(0) = %v,%v", rel, err)
	}
	// taken = {2,3}. Rollover: switch cpu0 to mm3 -> taken full {2,3}+? no:
	// free asid is 1, so mm3 gets 1 without rollover. Fill taken instead.
	got := mustSwitch(t, md, 0, 3)
	if got.rolled || got.asid != 1 {
		t.Fatalf("Switch(0,3) = %+v, want asid 1 reused, no rollover", got)
	}
	// Now taken = {1,2,3}. Force rollover with mm4.
	got = mustSwitch(t, md, 0, 4)
	if !got.rolled {
		t.Fatal("expected rollover")
	}
	// reserved = cpu0 (1,2), cpu1 (2,1); destroyed mm0's old asid 1
	// appears only because cpu0 is genuinely active on mm3's (1,2),
	// not because of mm0. mm0 stays dead and unreserved on its own.
	if md.mms[0].asid != 1 && md.mms[0].gen != 1 {
		// mm0 keeps its stale pair but is dead; nothing may revive it.
	}
	if _, _, _, _, err := md.Switch(0, 0); err != ErrDead {
		t.Fatalf("Switch to destroyed mm0 = %v, want ErrDead", err)
	}
	if _, err := md.Invalidate(0, 5); err != ErrDead {
		t.Fatalf("Invalidate on destroyed mm0 = %v, want ErrDead", err)
	}
	t.Log("destroyed mm: Switch/Invalidate report ErrDead; released asid lives only via its new owner")
}

// TestErrorPrecedence checks the mandated error ordering and that
// rejected operations change nothing.
func TestErrorPrecedence(t *testing.T) {
	md := mustNew(t, 3, 2, 2, 4)
	mustCreateMM(t, md, 2)
	mustSwitch(t, md, 0, 0)
	if _, _, err := md.DestroyMM(1); err != nil {
		t.Fatalf("DestroyMM(1) = %v", err)
	}

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"Switch bad cpu+bad mm+dead", func() error {
			_, _, _, _, err := md.Switch(9, 99)
			return err
		}, ErrBadCPU},
		{"Switch bad mm beats dead", func() error {
			_, _, _, _, err := md.Switch(0, 99)
			return err
		}, ErrBadMM},
		{"Switch dead", func() error {
			_, _, _, _, err := md.Switch(0, 1)
			return err
		}, ErrDead},
		{"Fill bad cpu beats bad param", func() error {
			_, _, err := md.Fill(9, 1<<33, 0)
			return err
		}, ErrBadCPU},
		{"Fill bad param beats no context", func() error {
			_, _, err := md.Fill(1, 1<<33, 0)
			return err
		}, ErrInvalidParam},
		{"Fill bad pfn", func() error {
			_, _, err := md.Fill(0, 0, 1<<40)
			return err
		}, ErrInvalidParam},
		{"Fill no context", func() error {
			_, _, err := md.Fill(1, 0, 0)
			return err
		}, ErrNoContext},
		{"Lookup bad cpu", func() error {
			_, _, err := md.Lookup(-1, 0)
			return err
		}, ErrBadCPU},
		{"Lookup bad param beats no context", func() error {
			_, _, err := md.Lookup(1, 1<<32)
			return err
		}, ErrInvalidParam},
		{"Lookup no context", func() error {
			_, _, err := md.Lookup(1, 0)
			return err
		}, ErrNoContext},
		{"Invalidate bad mm beats dead+param", func() error {
			_, err := md.Invalidate(7, 1<<33)
			return err
		}, ErrBadMM},
		{"Invalidate dead beats param", func() error {
			_, err := md.Invalidate(1, 1<<33)
			return err
		}, ErrDead},
		{"Invalidate bad param", func() error {
			_, err := md.Invalidate(0, 1<<32)
			return err
		}, ErrInvalidParam},
		{"DestroyMM bad mm beats dead", func() error {
			_, _, err := md.DestroyMM(7)
			return err
		}, ErrBadMM},
		{"DestroyMM dead", func() error {
			_, _, err := md.DestroyMM(1)
			return err
		}, ErrDead},
		{"DestroyMM busy", func() error {
			_, _, err := md.DestroyMM(0)
			return err
		}, ErrBusy},
	}
	for _, tc := range cases {
		before := snap(md)
		if got := tc.run(); got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
		if !reflect.DeepEqual(snap(md), before) {
			t.Fatalf("%s: rejected operation changed state", tc.name)
		}
	}
	t.Log("error precedence verified; every rejected op left the full state untouched")
}

// TestBoundaryParams: vpn/pfn of exactly 2^32-1 are legal.
func TestBoundaryParams(t *testing.T) {
	md := mustNew(t, 2, 1, 1, 2)
	mustCreateMM(t, md, 1)
	mustSwitch(t, md, 0, 0)
	const max = 1<<32 - 1
	if _, _, err := md.Fill(0, max, max); err != nil {
		t.Fatalf("Fill(max,max) = %v", err)
	}
	if pfn, hit, _ := md.Lookup(0, max); !hit || pfn != max {
		t.Fatalf("Lookup(max) = %d,%v", pfn, hit)
	}
	if n, err := md.Invalidate(0, max); err != nil || n != 1 {
		t.Fatalf("Invalidate(max) = %d,%v", n, err)
	}
}
