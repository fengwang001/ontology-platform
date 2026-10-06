package ontology

import (
	"errors"
	"sync"
	"testing"
)

type testLogger struct {
	mu     sync.Mutex
	values []string
}

func (l *testLogger) Log(input, output, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.values = append(l.values, input+" => "+output+" ("+reason+")")
}

func testConfig(maxSize int) Config {
	return Config{
		PointerSize:       8,
		PointerAlignment:  8,
		MinCompositeSize:  0,
		MinCompositeAlign: 1,
		MaxSize:           maxSize,
		AllowedAlignments: map[int]bool{1: true, 2: true, 4: true, 8: true, 16: true},
	}
}

func basic(name string, size, alignment int) TypeSpec {
	return TypeSpec{Name: name, Basic: true, Size: size, Alignment: alignment}
}

func requireErrorKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	var typed *TypeError
	if !errors.As(err, &typed) || typed.Kind != kind {
		t.Fatalf("error = %v, want kind %d", err, kind)
	}
}

func registerMust(t *testing.T, r *Registry, spec TypeSpec) {
	t.Helper()
	if err := r.Register(spec); err != nil {
		t.Fatalf("register %s: %v", spec.Name, err)
	}
}

func TestAlignmentCompactAndCap(t *testing.T) {
	r := NewRegistry(testConfig(128))
	registerMust(t, r, basic("u32", 4, 4))
	registerMust(t, r, basic("u8", 1, 1))
	spec := TypeSpec{
		Name:     "S",
		MaxAlign: 2,
		Fields: []Field{
			{Name: "a", Type: "u32"},
			{Name: "b", Type: "u8", Compact: true},
			{Name: "c", Type: "u32"},
		},
	}
	registerMust(t, r, spec)
	layout, ok := r.Layout("S")
	if !ok {
		t.Fatal("S missing")
	}
	offsets := []int{0, 4, 6}
	for index, want := range offsets {
		if layout.Fields[index].Offset != want {
			t.Fatalf("field %d offset = %d, want %d", index, layout.Fields[index].Offset, want)
		}
	}
	if layout.Align != 2 || layout.Size != 10 {
		t.Fatalf("layout = size %d align %d, want 10/2", layout.Size, layout.Align)
	}
}

func TestEmptyAndPointersAndDirectCycle(t *testing.T) {
	r := NewRegistry(testConfig(128))
	registerMust(t, r, TypeSpec{Name: "Empty"})
	empty, _ := r.Layout("Empty")
	if empty.Size != 0 || empty.Align != 1 || empty.Version != 1 {
		t.Fatalf("empty layout = %+v", empty)
	}
	registerMust(t, r, TypeSpec{
		Name: "Node",
		Fields: []Field{
			{Name: "next", Type: "Future", Pointer: true},
			{Name: "self", Type: "Node", Pointer: true},
		},
	})
	node, _ := r.Layout("Node")
	if node.Size != 16 || node.Fields[0].Offset != 0 {
		t.Fatalf("node layout = %+v", node)
	}
	err := r.Register(TypeSpec{
		Name:   "Bad",
		Fields: []Field{{Name: "self", Type: "Bad"}},
	})
	requireErrorKind(t, err, ErrDirectCycle)
	if _, ok := r.Layout("Bad"); ok {
		t.Fatal("rejected direct cycle was recorded")
	}
}

func TestSizeBoundary(t *testing.T) {
	r := NewRegistry(testConfig(8))
	if err := r.Register(basic("Exact", 8, 4)); err != nil {
		t.Fatalf("exact-limit registration: %v", err)
	}
	err := r.Register(basic("Over", 9, 1))
	requireErrorKind(t, err, ErrSizeExceeded)
}

func TestCompatibilityConclusionsAndReorder(t *testing.T) {
	r := NewRegistry(testConfig(128))
	registerMust(t, r, basic("u32", 4, 4))
	registerMust(t, r, TypeSpec{Name: "A", Fields: []Field{{Name: "x", Type: "u32"}}})

	result, err := r.Modify("A", TypeSpec{Name: "A", Fields: []Field{{Name: "x", Type: "u32"}}})
	if err != nil || result.RootCompatibility != FullyCompatible {
		t.Fatalf("full compatibility result = %+v, %v", result, err)
	}
	view, _ := r.View("A")
	if view.Version != 1 || view.Recalculations != 1 {
		t.Fatalf("version/recalc = %d/%d", view.Version, view.Recalculations)
	}

	result, err = r.Modify("A", TypeSpec{Name: "A", Fields: []Field{
		{Name: "x", Type: "u32"},
		{Name: "y", Type: "u32"},
	}})
	if err != nil || result.RootCompatibility != AppendCompatible {
		t.Fatalf("append compatibility result = %+v, %v", result, err)
	}

	result, err = r.Modify("A", TypeSpec{Name: "A", Fields: []Field{
		{Name: "y", Type: "u32"},
		{Name: "x", Type: "u32"},
	}})
	if err != nil || result.RootCompatibility != Incompatible {
		t.Fatalf("reorder result = %+v, %v", result, err)
	}
}

func TestCompatibilityIncompatibleIdentityChanges(t *testing.T) {
	r := NewRegistry(testConfig(128))
	registerMust(t, r, basic("u32", 4, 4))
	registerMust(t, r, basic("u8", 1, 1))
	original := TypeSpec{Name: "A", Fields: []Field{
		{Name: "old", Type: "u32"},
		{Name: "tail", Type: "u32"},
	}}
	registerMust(t, r, original)

	renamed := original
	renamed.Fields[0].Name = "new"
	result, err := r.Modify("A", renamed)
	if err != nil || result.RootCompatibility != Incompatible {
		t.Fatalf("rename result = %+v, %v", result, err)
	}

	compact := original
	compact.Fields[1] = Field{Name: "tail", Type: "u8", Compact: true}
	result, err = r.Modify("A", compact)
	if err != nil || result.RootCompatibility != Incompatible {
		t.Fatalf("compact change result = %+v, %v", result, err)
	}

	registerMust(t, r, TypeSpec{Name: "Aligned", Fields: []Field{{Name: "x", Type: "u8"}}})
	result, err = r.Modify("Aligned", basic("Aligned", 1, 8))
	if err != nil || result.RootCompatibility != Incompatible {
		t.Fatalf("alignment result = %+v, %v", result, err)
	}
}

func TestPropagationStopsAtFullCompatibility(t *testing.T) {
	r := NewRegistry(testConfig(128))
	registerMust(t, r, basic("u32", 4, 4))
	registerMust(t, r, TypeSpec{Name: "A", Fields: []Field{
		{Name: "x", Type: "u32"},
		{Name: "y", Type: "u32"},
	}})
	registerMust(t, r, TypeSpec{Name: "B", Fields: []Field{{Name: "a", Type: "A"}}})
	registerMust(t, r, TypeSpec{Name: "C", Fields: []Field{{Name: "b", Type: "B"}}})

	result, err := r.Modify("A", TypeSpec{Name: "A", Fields: []Field{
		{Name: "y", Type: "u32"},
		{Name: "x", Type: "u32"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Compatibility["B"] != FullyCompatible {
		t.Fatalf("B compatibility = %v", result.Compatibility["B"])
	}
	if len(result.Recalculated) != 2 || result.Recalculated[0] != "A" || result.Recalculated[1] != "B" {
		t.Fatalf("recalculated = %v", result.Recalculated)
	}
	cView, _ := r.View("C")
	if cView.Version != 1 || cView.Recalculations != 0 {
		t.Fatalf("C unexpectedly recalculated: %+v", cView)
	}
}

func TestDependentOverflowRollsBack(t *testing.T) {
	r := NewRegistry(testConfig(8))
	registerMust(t, r, basic("u32", 4, 4))
	registerMust(t, r, basic("A", 4, 4))
	registerMust(t, r, TypeSpec{Name: "D", Fields: []Field{
		{Name: "a", Type: "A"},
		{Name: "b", Type: "u32"},
	}})
	_, err := r.Modify("A", basic("A", 8, 4))
	requireErrorKind(t, err, ErrDependentSizeExceeded)
	a, _ := r.Layout("A")
	d, _ := r.Layout("D")
	view, _ := r.View("A")
	if a.Size != 4 || d.Size != 8 || view.Version != 1 {
		t.Fatalf("rollback failed A=%+v D=%+v", a, d)
	}
}

func TestErrorOrder(t *testing.T) {
	r := NewRegistry(testConfig(0))
	err := r.Register(TypeSpec{Name: "UndefFirst", MaxAlign: 3, Fields: []Field{
		{Name: "x", Type: "missing"},
		{Name: "x", Type: "missing"},
	}})
	requireErrorKind(t, err, ErrUndefinedType)

	err = r.Register(TypeSpec{Name: "AlignBeforeDup", MaxAlign: 3, Fields: []Field{
		{Name: "x", Type: "u32", Pointer: true},
		{Name: "x", Type: "u32", Pointer: true},
	}})
	requireErrorKind(t, err, ErrInvalidAlignment)

	r.config = testConfig(128)
	registerMust(t, r, basic("u32", 4, 4))
	err = r.Register(TypeSpec{Name: "DupBeforeCycle", Fields: []Field{
		{Name: "x", Type: "DupBeforeCycle"},
		{Name: "x", Type: "u32"},
	}})
	requireErrorKind(t, err, ErrDuplicateField)

	err = r.Register(TypeSpec{Name: "CycleBeforeSize", Fields: []Field{
		{Name: "x", Type: "CycleBeforeSize"},
	}})
	requireErrorKind(t, err, ErrDirectCycle)
}

func TestConcurrentRegisterAndQuery(t *testing.T) {
	r := NewRegistry(testConfig(128))
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			name := "T" + string(rune('a'+index%26)) + string(rune('a'+index/26))
			_ = r.Register(basic(name, 4, 4))
			_, _ = r.Layout(name)
			_, _ = r.View(name)
		}(index)
	}
	wait.Wait()
	if len(r.Views()) != 64 {
		t.Fatalf("views = %d, want 64", len(r.Views()))
	}
}

func TestLoggerReceivesDecision(t *testing.T) {
	logger := &testLogger{}
	config := testConfig(128)
	config.Logger = logger
	r := NewRegistry(config)
	registerMust(t, r, basic("u32", 4, 4))
	registerMust(t, r, TypeSpec{Name: "A", Fields: []Field{{Name: "x", Type: "u32"}}})
	if _, err := r.Modify("A", TypeSpec{Name: "A"}); err != nil {
		t.Fatal(err)
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if len(logger.values) < 3 {
		t.Fatalf("logs = %v", logger.values)
	}
}
