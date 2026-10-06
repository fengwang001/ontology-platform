package layout

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
)

// testLogger writes each decision line to a mutex-guarded writer so tests can
// demonstrate the required "input / output / reason" log.
type testLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *testLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, format+"\n", args...)
}

func newTestRegistry(t *testing.T, cfg Config) *Registry {
	t.Helper()
	r, err := NewRegistry(cfg, &testLogger{w: os.Stdout})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func mustBasic(tb testing.TB, r *Registry, name string, size, align int) {
	tb.Helper()
	if err := r.RegisterBasic(name, size, align); err != nil {
		tb.Fatalf("RegisterBasic %s: %v", name, err)
	}
}

func mustRegister(t *testing.T, r *Registry, spec CompositeSpec) *Layout {
	t.Helper()
	if err := r.Register(spec); err != nil {
		t.Fatalf("Register %s: %v", spec.Name, err)
	}
	l, err := r.Get(spec.Name)
	if err != nil {
		t.Fatalf("Get %s: %v", spec.Name, err)
	}
	return l
}

func TestBasicAlignmentAndPadding(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u8", 1, 1)
	mustBasic(t, r, "u64", 8, 8)
	l := mustRegister(t, r, CompositeSpec{Name: "S", Fields: []Field{
		{Name: "a", TypeName: "u8"},
		{Name: "b", TypeName: "u64"},
		{Name: "c", TypeName: "u8"},
	}})
	// offsets: a=0, b padded to 8, c=16; tail padding rounds 17 -> 24.
	wantOff := []int{0, 8, 16}
	for i, want := range wantOff {
		if l.Fields[i].Offset != want {
			t.Fatalf("field %s offset = %d, want %d", l.Fields[i].Name, l.Fields[i].Offset, want)
		}
	}
	if l.Size != 24 || l.Align != 8 {
		t.Fatalf("size/align = %d/%d, want 24/8", l.Size, l.Align)
	}
}

func TestCompactFields(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u8", 1, 1)
	mustBasic(t, r, "u64", 8, 8)
	l := mustRegister(t, r, CompositeSpec{Name: "S", Fields: []Field{
		{Name: "a", TypeName: "u8"},
		{Name: "b", TypeName: "u64", Compact: true}, // no alignment, packed at 1
		{Name: "c", TypeName: "u8"},                 // aligns to b's effective? no: c aligns to 1 -> offset 9
	}})
	if l.Fields[1].Offset != 1 {
		t.Fatalf("compact b offset = %d, want 1", l.Fields[1].Offset)
	}
	if l.Fields[2].Offset != 9 {
		t.Fatalf("c offset = %d, want 9", l.Fields[2].Offset)
	}
	// b is compact and excluded from the type alignment; max non-compact is 1.
	if l.Align != 1 {
		t.Fatalf("align = %d, want 1", l.Align)
	}
	if l.Size != 10 {
		t.Fatalf("size = %d, want 10", l.Size)
	}
}

func TestMaxAlignCap(t *testing.T) {
	cfg := DefaultConfig()
	r := newTestRegistry(t, cfg)
	mustBasic(t, r, "u32", 4, 4)
	mustBasic(t, r, "u64", 8, 8)
	// Cap 4: the 8-aligned field is placed at offset 4; type alignment is 4.
	l := mustRegister(t, r, CompositeSpec{Name: "S", MaxAlign: 4, Fields: []Field{
		{Name: "a", TypeName: "u32"},
		{Name: "b", TypeName: "u64"},
	}})
	if l.Fields[1].Offset != 4 {
		t.Fatalf("b offset = %d, want 4", l.Fields[1].Offset)
	}
	if l.Align != 4 || l.Size != 12 {
		t.Fatalf("size/align = %d/%d, want 12/4", l.Size, l.Align)
	}

	// Illegal cap.
	if err := r.Register(CompositeSpec{Name: "Bad", MaxAlign: 3, Fields: []Field{
		{Name: "x", TypeName: "u32"},
	}}); !errors.Is(err, ErrInvalidAlignment) {
		t.Fatalf("MaxAlign 3: err = %v, want ErrInvalidAlignment", err)
	}
}

func TestEmptyComposite(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	l := mustRegister(t, r, CompositeSpec{Name: "Empty"})
	if l.Size != r.cfg.EmptySize || l.Align != r.cfg.EmptyAlign {
		t.Fatalf("empty size/align = %d/%d, want %d/%d", l.Size, l.Align, r.cfg.EmptySize, r.cfg.EmptyAlign)
	}
	v, err := r.View("Empty")
	if err != nil || v.Version != 1 || v.Size != l.Size {
		t.Fatalf("view = %+v, err = %v", v, err)
	}
}

func TestSizeLimitBoundaries(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSize = 8
	r := newTestRegistry(t, cfg)
	mustBasic(t, r, "u8", 1, 1)
	mustBasic(t, r, "u32", 4, 4)

	// Equal to the limit: accepted.
	mustRegister(t, r, CompositeSpec{Name: "Eq", Fields: []Field{
		{Name: "a", TypeName: "u32"},
		{Name: "b", TypeName: "u32"},
	}})
	// One byte beyond: rejected and no record remains.
	err := r.Register(CompositeSpec{Name: "Over", Fields: []Field{
		{Name: "a", TypeName: "u32"},
		{Name: "b", TypeName: "u32"},
		{Name: "c", TypeName: "u8"},
	}})
	if !errors.Is(err, ErrSizeExceeded) {
		t.Fatalf("err = %v, want ErrSizeExceeded", err)
	}
	if _, err := r.Get("Over"); !errors.Is(err, ErrUndefinedType) {
		t.Fatalf("rejected type left a record: %v", err)
	}
}

func TestIndirectRefsAndCycles(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u8", 1, 1)
	// Indirect field may point at an unregistered type.
	mustRegister(t, r, CompositeSpec{Name: "A", Fields: []Field{
		{Name: "next", TypeName: "B", Indirect: true},
	}})
	// Legal self cycle via indirection.
	mustRegister(t, r, CompositeSpec{Name: "Node", Fields: []Field{
		{Name: "self", TypeName: "Node", Indirect: true},
	}})
	// Mutual cycle via indirection.
	mustRegister(t, r, CompositeSpec{Name: "L", Fields: []Field{
		{Name: "r", TypeName: "R", Indirect: true},
	}})
	mustRegister(t, r, CompositeSpec{Name: "R", Fields: []Field{
		{Name: "l", TypeName: "L", Indirect: true},
	}})

	// Direct self embedding: rejected.
	if err := r.Register(CompositeSpec{Name: "D", Fields: []Field{
		{Name: "me", TypeName: "D"},
	}}); !errors.Is(err, ErrEmbeddingCycle) {
		t.Fatalf("self embed: err = %v, want ErrEmbeddingCycle", err)
	}
	// Direct mutual embedding: X embeds Y (registered first), then modifying Y
	// to embed X creates the cycle and must be rejected.
	mustRegister(t, r, CompositeSpec{Name: "Y", Fields: []Field{
		{Name: "pad", TypeName: "u8"},
	}})
	mustRegister(t, r, CompositeSpec{Name: "X", Fields: []Field{
		{Name: "y", TypeName: "Y"},
	}})
	if _, err := r.Modify(CompositeSpec{Name: "Y", Fields: []Field{
		{Name: "x", TypeName: "X"},
	}}); !errors.Is(err, ErrEmbeddingCycle) {
		t.Fatalf("mutual embed: err = %v, want ErrEmbeddingCycle", err)
	}

	// Undefined direct target.
	if err := r.Register(CompositeSpec{Name: "U", Fields: []Field{
		{Name: "ghost", TypeName: "NoSuchType"},
	}}); !errors.Is(err, ErrUndefinedType) {
		t.Fatalf("undefined target: err = %v, want ErrUndefinedType", err)
	}

	// Duplicate field name.
	if err := r.Register(CompositeSpec{Name: "Dup", Fields: []Field{
		{Name: "f", TypeName: "u8"},
		{Name: "f", TypeName: "u8"},
	}}); !errors.Is(err, ErrDuplicateField) {
		t.Fatalf("dup field: err = %v, want ErrDuplicateField", err)
	}
}

func TestDependentCountFollowsEdgeChanges(t *testing.T) {
	r := newTestRegistry(t, DefaultConfig())
	mustBasic(t, r, "u8", 1, 1)
	mustRegister(t, r, CompositeSpec{Name: "B", Fields: []Field{{Name: "x", TypeName: "u8"}}})
	mustRegister(t, r, CompositeSpec{Name: "C", Fields: []Field{{Name: "x", TypeName: "u8"}}})
	mustRegister(t, r, CompositeSpec{Name: "A", Fields: []Field{
		{Name: "b", TypeName: "B"},
	}})
	if v, _ := r.View("B"); v.Dependents != 1 {
		t.Fatalf("B dependents = %d, want 1", v.Dependents)
	}
	// Even a fully-compatible redeclaration that swaps the embedded target
	// must refresh reverse edges.
	if _, err := r.Modify(CompositeSpec{Name: "A", Fields: []Field{
		{Name: "b", TypeName: "C"},
	}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.View("B"); v.Dependents != 0 {
		t.Fatalf("B dependents after edge move = %d, want 0", v.Dependents)
	}
	if v, _ := r.View("C"); v.Dependents != 1 {
		t.Fatalf("C dependents after edge move = %d, want 1", v.Dependents)
	}
}
