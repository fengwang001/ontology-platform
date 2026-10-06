package layout

import (
	"errors"
	"testing"
)

func TestCompatVerdicts(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u8", 1, 1)
	mustBasic(t, r, "u32", 4, 4)
	mustBasic(t, r, "u64", 8, 8)
	mustRegister(t, r, CompositeSpec{Name: "S", Fields: []Field{
		{Name: "a", TypeName: "u8"},
		{Name: "b", TypeName: "u32"},
	}})
	orig, _ := r.Get("S")

	// Identical redeclaration: fully compatible, no version bump.
	res, err := r.Modify(CompositeSpec{Name: "S", Fields: []Field{
		{Name: "a", TypeName: "u8"},
		{Name: "b", TypeName: "u32"},
	}})
	if err != nil || res.Root != FullyCompatible {
		t.Fatalf("identical: res=%+v err=%v", res, err)
	}
	v, _ := r.View("S")
	if v.Version != 1 || v.RecomputeCount != 0 {
		t.Fatalf("full-compat version/recompute = %d/%d, want 1/0", v.Version, v.RecomputeCount)
	}

	// Trailing append, size grows, alignment stable: append compatible.
	res, err = r.Modify(CompositeSpec{Name: "S", Fields: []Field{
		{Name: "a", TypeName: "u8"},
		{Name: "b", TypeName: "u32"},
		{Name: "c", TypeName: "u8"},
	}})
	if err != nil || res.Root != AppendCompatible {
		t.Fatalf("append: res=%+v err=%v", res, err)
	}
	if got, _ := r.Get("S"); got.Size <= orig.Size {
		t.Fatalf("appended size = %d, want > %d", got.Size, orig.Size)
	}

	// Rename an old field: incompatible even if layout numbers stay equal.
	res, err = r.Modify(CompositeSpec{Name: "S", Fields: []Field{
		{Name: "a", TypeName: "u8"},
		{Name: "renamed", TypeName: "u32"},
		{Name: "c", TypeName: "u8"},
	}})
	if err != nil || res.Root != Incompatible {
		t.Fatalf("rename: res=%+v err=%v", res, err)
	}

	// Compact flag change: incompatible.
	res, _ = r.Modify(CompositeSpec{Name: "S", Fields: []Field{
		{Name: "a", TypeName: "u8", Compact: true},
		{Name: "renamed", TypeName: "u32"},
		{Name: "c", TypeName: "u8"},
	}})
	if res.Root != Incompatible {
		t.Fatalf("compact change: %v", res.Root)
	}
}

func TestSwapIsIncompatibleDespiteCoincidentLayout(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u32", 4, 4)
	// Two fields of identical size/alignment: swapping them leaves offsets
	// byte-for-byte identical but must still be incompatible.
	mustRegister(t, r, CompositeSpec{Name: "Pair", Fields: []Field{
		{Name: "x", TypeName: "u32"},
		{Name: "y", TypeName: "u32"},
	}})
	res, err := r.Modify(CompositeSpec{Name: "Pair", Fields: []Field{
		{Name: "y", TypeName: "u32"},
		{Name: "x", TypeName: "u32"},
	}})
	if err != nil {
		t.Fatalf("modify: %v", err)
	}
	if res.Root != Incompatible {
		t.Fatalf("swap verdict = %v, want incompatible", res.Root)
	}
}

func TestAlignmentChangeIsIncompatible(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u32", 4, 4)
	mustBasic(t, r, "u64", 8, 8)
	mustRegister(t, r, CompositeSpec{Name: "S", MaxAlign: 4, Fields: []Field{
		{Name: "a", TypeName: "u64"},
	}})
	res, err := r.Modify(CompositeSpec{Name: "S", MaxAlign: 8, Fields: []Field{
		{Name: "a", TypeName: "u64"},
	}})
	if err != nil || res.Root != Incompatible {
		t.Fatalf("align cap change: res=%+v err=%v", res, err)
	}
	if l, _ := r.Get("S"); l.Align != 8 {
		t.Fatalf("new align = %d, want 8", l.Align)
	}
}

func TestPropagationStopsAtFullCompatibility(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u8", 1, 1)
	mustBasic(t, r, "u32", 4, 4)
	mustBasic(t, r, "u64", 8, 8)

	// A embeds B; C embeds A (chain B <- A <- C).
	mustRegister(t, r, CompositeSpec{Name: "B", Fields: []Field{
		{Name: "x", TypeName: "u32"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "A", Fields: []Field{
		{Name: "b", TypeName: "B"},
		{Name: "p", TypeName: "u8"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "C", Fields: []Field{
		{Name: "a", TypeName: "A"},
	}})
	// Indirect referrer must be untouched.
	mustRegister(t, r, CompositeSpec{Name: "P", Fields: []Field{
		{Name: "b", TypeName: "B", Indirect: true},
	}})

	// Scenario 1 (stop at full compatibility):
	// B is {compact u32 at 0, compact u8 at 4} = 5 bytes, padded to 8.
	// Turning the u8 non-compact moves it to offset 8 and grows B to 12...
	// Instead, two versions of B share size 8/alignment 4 while differing
	// internally: v1 packs {u8@0, u32 compact@1}; v2 is {u32@0, u8@4}.
	// B's own verdict is incompatible (offsets moved), so propagation runs;
	// but B's size/alignment are unchanged, so A2 that embeds only B sees the
	// identical (offset 0, size 8, align 4) field and is fully compatible:
	// propagation must stop before C2.
	mustRegister(t, r, CompositeSpec{Name: "B2", Fields: []Field{
		{Name: "x", TypeName: "u8"},
		{Name: "y", TypeName: "u32", Compact: true}, // packed at 1; 1..5
		{Name: "z", TypeName: "u32"},                // at 8, gives align 4
	}})
	mustRegister(t, r, CompositeSpec{Name: "A2", Fields: []Field{
		{Name: "b", TypeName: "B2"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "C2", Fields: []Field{
		{Name: "a", TypeName: "A2"},
	}})
	beforeA, _ := r.View("A2")
	beforeC, _ := r.View("C2")
	beforeP, _ := r.View("P")
	res, err := r.Modify(CompositeSpec{Name: "B2", Fields: []Field{
		{Name: "x", TypeName: "u32"}, // at 0
		{Name: "y", TypeName: "u8"},  // at 4
		{Name: "z", TypeName: "u32"}, // at 8
	}})
	if err != nil {
		t.Fatalf("modify B2: %v", err)
	}
	if res.Root != Incompatible || len(res.Recomputed) != 0 {
		t.Fatalf("stop-at-full: root=%v recomputed=%v", res.Root, res.Recomputed)
	}
	if v, _ := r.View("A2"); v.Version != beforeA.Version {
		t.Fatalf("A2 fully compatible but version bumped")
	}
	if v, _ := r.View("C2"); v.Version != beforeC.Version {
		t.Fatalf("C2 recomputed though A2 was fully compatible")
	}
	if v, _ := r.View("P"); v.Version != beforeP.Version {
		t.Fatalf("indirect referrer P was recomputed")
	}

	// Scenario 2 (real upward propagation): B itself grows in a way that
	// changes A's embedding (B 4 -> 12), so A and then C both recompute.
	mustRegister(t, r, CompositeSpec{Name: "B3", Fields: []Field{
		{Name: "x", TypeName: "u32"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "A3", Fields: []Field{
		{Name: "b", TypeName: "B3"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "C3", Fields: []Field{
		{Name: "a", TypeName: "A3"},
	}})
	beforeC3, _ := r.View("C3")
	res, err = r.Modify(CompositeSpec{Name: "B3", Fields: []Field{
		{Name: "x", TypeName: "u32"},
		{Name: "y", TypeName: "u8"},
		{Name: "z", TypeName: "u32"},
	}})
	if err != nil {
		t.Fatalf("modify B3: %v", err)
	}
	if len(res.Recomputed) != 2 {
		t.Fatalf("Recomputed = %v, want [A3 C3]", res.Recomputed)
	}
	if res.Recomputed[0] != "A3" || res.Recomputed[1] != "C3" {
		t.Fatalf("propagation order = %v, want [A3 C3]", res.Recomputed)
	}
	if v, _ := r.View("C3"); v.Version != beforeC3.Version+1 {
		t.Fatalf("C3 version not bumped")
	}
}

func TestModifyDependentOversizeRollback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSize = 16
	r := newTestRegistry(t, cfg)
	mustBasic(t, r, "u32", 4, 4)
	mustRegister(t, r, CompositeSpec{Name: "B", Fields: []Field{
		{Name: "x", TypeName: "u32"},
		{Name: "y", TypeName: "u32"},
	}})
	// A embeds B twice: A is exactly 16. Appending 8 bytes to B makes each
	// embedded copy 16 bytes, forcing A to 32 > 16.
	mustRegister(t, r, CompositeSpec{Name: "A", Fields: []Field{
		{Name: "b1", TypeName: "B"},
		{Name: "b2", TypeName: "B"},
	}})
	snapB, _ := r.View("B")
	snapA, _ := r.View("A")

	_, err := r.Modify(CompositeSpec{Name: "B", Fields: []Field{
		{Name: "x", TypeName: "u32"},
		{Name: "y", TypeName: "u32"},
		{Name: "z", TypeName: "u32"},
	}})
	if !errors.Is(err, ErrDependentOversize) {
		t.Fatalf("err = %v, want ErrDependentOversize", err)
	}
	// Everything stays at the pre-modification instant.
	if v, _ := r.View("B"); v.Size != snapB.Size || v.Version != snapB.Version {
		t.Fatalf("B mutated despite rollback: %+v vs %+v", v, snapB)
	}
	if v, _ := r.View("A"); v.Size != snapA.Size || v.Version != snapA.Version {
		t.Fatalf("A mutated despite rollback: %+v vs %+v", v, snapA)
	}
}

func TestOnlyReachableDependentsRecompute(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u32", 4, 4)
	// head is embedded by leafA (live dependent) and leafI (indirect only).
	mustRegister(t, r, CompositeSpec{Name: "head", Fields: []Field{
		{Name: "x", TypeName: "u32"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "leafA", Fields: []Field{
		{Name: "h", TypeName: "head"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "leafI", Fields: []Field{
		{Name: "h", TypeName: "head", Indirect: true},
	}})
	// A totally unrelated type with its own dependency chain.
	mustRegister(t, r, CompositeSpec{Name: "other", Fields: []Field{
		{Name: "x", TypeName: "u32"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "otherChild", Fields: []Field{
		{Name: "o", TypeName: "other"},
	}})

	res, err := r.Modify(CompositeSpec{Name: "head", Fields: []Field{
		{Name: "x", TypeName: "u32"},
		{Name: "y", TypeName: "u32"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Recomputed) != 1 || res.Recomputed[0] != "leafA" {
		t.Fatalf("Recomputed = %v, want [leafA]", res.Recomputed)
	}
	for _, n := range []string{"leafI", "other", "otherChild"} {
		v, _ := r.View(n)
		if v.RecomputeCount != 0 || v.Version != 1 {
			t.Fatalf("%q unexpectedly recomputed: %+v", n, v)
		}
	}
	va, _ := r.View("leafA")
	if va.RecomputeCount != 1 || va.Version != 2 {
		t.Fatalf("leafA view = %+v, want version 2 / 1 recompute", va)
	}
}

func TestRejectionOrder(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u64", 8, 8)
	// Undefined beats duplicate + illegal align + size.
	err := r.Register(CompositeSpec{Name: "S", Fields: []Field{
		{Name: "f", TypeName: "Ghost"},
		{Name: "f", TypeName: "u64"},
	}})
	if !errors.Is(err, ErrUndefinedType) {
		t.Fatalf("undefined priority: %v", err)
	}
	// Illegal alignment beats duplicate field: allow the pointer/empty
	// alignments but forbid the composite cap.
	cfg := DefaultConfig()
	cfg.AllowedAlignments = map[int]bool{1: true, 8: true} // cap 2 forbidden
	r2 := newTestRegistry(t, cfg)
	mustBasic(t, r2, "u1", 1, 1)
	if err := r2.RegisterBasic("u4", 4, 4); !errors.Is(err, ErrInvalidAlignment) {
		t.Fatalf("basic illegal align: %v", err)
	}
	err = r2.Register(CompositeSpec{Name: "S", MaxAlign: 2, Fields: []Field{
		{Name: "f", TypeName: "u1"},
		{Name: "f", TypeName: "u1"},
	}})
	if !errors.Is(err, ErrInvalidAlignment) {
		t.Fatalf("illegal cap must precede duplicate: %v", err)
	}
}
