package resolver

import (
	"fmt"
	"strings"
	"testing"
)

func nest(n int, base *Type) *Type {
	ty := base
	for i := 0; i < n; i++ {
		ty = Con("List", ty)
	}
	return ty
}

func wantErr(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %v, got nil error", kind)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if e.Kind != kind {
		t.Fatalf("got %v (%s), want %v", e.Kind, e.Detail, kind)
	}
}

func TestDepthLimitRange(t *testing.T) {
	for _, d := range []int{1, 64} {
		if _, err := NewResolver(d); err != nil {
			t.Fatalf("NewResolver(%d): %v", d, err)
		}
	}
	for _, d := range []int{0, -1, 65, 100} {
		_, err := NewResolver(d)
		wantErr(t, err, ErrInvalidParam)
	}
}

func TestNameLimits(t *testing.T) {
	r := mustResolver(t, 8)

	_, err := r.AddInstance("", Con("Int"), nil)
	wantErr(t, err, ErrInvalidParam)
	_, err = r.AddInstance(strings.Repeat("x", 33), Con("Int"), nil)
	wantErr(t, err, ErrInvalidParam)
	if _, err := r.AddInstance(strings.Repeat("x", 32), Con("Int"), nil); err != nil {
		t.Fatalf("32-byte trait name must be accepted: %v", err)
	}

	_, err = r.AddInstance("F", Con(""), nil)
	wantErr(t, err, ErrInvalidParam)
	_, err = r.AddInstance("F", Con(strings.Repeat("y", 33)), nil)
	wantErr(t, err, ErrInvalidParam)
	if _, err := r.AddInstance("F", Con(strings.Repeat("y", 32)), nil); err != nil {
		t.Fatalf("32-byte constructor name must be accepted: %v", err)
	}

	if _, _, err := r.Resolve("", Con("Int")); err == nil {
		t.Fatal("empty trait name must be rejected")
	}
	if _, _, err := r.Resolve("F", Con(strings.Repeat("z", 33))); err == nil {
		t.Fatal("overlong constructor name must be rejected")
	}
}

func TestArgCountLimit(t *testing.T) {
	r := mustResolver(t, 8)
	args4 := []*Type{Con("Int"), Con("Int"), Con("Int"), Con("Int")}
	if _, err := r.AddInstance("F", Con("Q", args4...), nil); err != nil {
		t.Fatalf("4 arguments must be accepted: %v", err)
	}
	args5 := []*Type{Con("Int"), Con("Int"), Con("Int"), Con("Int"), Con("Int")}
	_, err := r.AddInstance("F", Con("Q", args5...), nil)
	wantErr(t, err, ErrInvalidParam)
}

func TestVarIndexLimit(t *testing.T) {
	r := mustResolver(t, 8)
	if _, err := r.AddInstance("F", Con("List", Var(7)), nil); err != nil {
		t.Fatalf("Var(7) must be accepted: %v", err)
	}
	_, err := r.AddInstance("F", Con("List", Var(8)), nil)
	wantErr(t, err, ErrInvalidParam)
	_, err = r.AddInstance("F", &Type{IsVar: true, Var: -1}, nil)
	wantErr(t, err, ErrInvalidParam)
}

func TestGroundTypeValidation(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("Int"))

	_, _, err := r.Resolve("Show", Con("List", Var(0)))
	wantErr(t, err, ErrInvalidParam)

	// Depth 16 is allowed, depth 17 is rejected.
	if _, _, err := r.Resolve("Show", nest(15, Con("Int"))); err != nil {
		t.Fatalf("depth 16 must be accepted: %v", err)
	}
	_, _, err = r.Resolve("Show", nest(16, Con("Int")))
	wantErr(t, err, ErrInvalidParam)
}

func TestContextLimits(t *testing.T) {
	r := mustResolver(t, 8)

	ctx4 := []Constraint{
		{Trait: "A", Type: Var(0)},
		{Trait: "B", Type: Var(0)},
		{Trait: "C", Type: Var(0)},
		{Trait: "D", Type: Var(0)},
	}
	if _, err := r.AddInstance("F", Con("List", Var(0)), ctx4); err != nil {
		t.Fatalf("4 constraints must be accepted: %v", err)
	}
	ctx5 := append(append([]Constraint{}, ctx4...), Constraint{Trait: "E", Type: Var(0)})
	_, err := r.AddInstance("G", Con("List", Var(0)), ctx5)
	wantErr(t, err, ErrInvalidParam)

	// A context variable that does not occur in the head is rejected.
	_, err = r.AddInstance("H", Con("List", Var(0)), []Constraint{{Trait: "A", Type: Var(1)}})
	wantErr(t, err, ErrInvalidParam)

	// A context constraint with an invalid trait name is rejected.
	_, err = r.AddInstance("I", Con("List", Var(0)), []Constraint{{Trait: "", Type: Var(0)}})
	wantErr(t, err, ErrInvalidParam)
}

func TestInstanceLimit(t *testing.T) {
	r := mustResolver(t, 8)
	for i := 1; i <= MaxInstances; i++ {
		id, err := r.AddInstance("F", Con(fmt.Sprintf("C%03d", i)), nil)
		if err != nil {
			t.Fatalf("instance %d must be accepted: %v", i, err)
		}
		if id != i {
			t.Fatalf("instance id: got %d, want %d", id, i)
		}
	}
	_, err := r.AddInstance("F", Con("C201"), nil)
	wantErr(t, err, ErrInstanceLimit)
}

func TestDuplicateInstanceByRenaming(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "F", Con("Pair", Var(0), Var(1))) // 1

	// Same head up to variable renaming.
	_, err := r.AddInstance("F", Con("Pair", Var(3), Var(5)), nil)
	wantErr(t, err, ErrDuplicateInstance)
	// Swapped names normalize identically.
	_, err = r.AddInstance("F", Con("Pair", Var(1), Var(0)), nil)
	wantErr(t, err, ErrDuplicateInstance)

	// A non-linear head is not a duplicate of a linear one.
	mustAdd(t, r, "F", Con("Pair", Var(0), Var(0))) // 2
	_, err = r.AddInstance("F", Con("Pair", Var(2), Var(2)), nil)
	wantErr(t, err, ErrDuplicateInstance)

	// The duplicate check ignores the context.
	_, err = r.AddInstance("F", Con("Pair", Var(2), Var(3)), []Constraint{{Trait: "G", Type: Var(2)}})
	wantErr(t, err, ErrDuplicateInstance)

	// Same head under a different trait is fine.
	mustAdd(t, r, "G", Con("Pair", Var(0), Var(1))) // 3

	// Rejections do not consume instance ids.
	if id := mustAdd(t, r, "H", Con("Int")); id != 4 {
		t.Fatalf("instance id: got %d, want 4", id)
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("Int"))                                                   // 1
	mustAdd(t, r, "Show", Con("List", Var(0)), Constraint{Trait: "Show", Type: Var(0)}) // 2
	mustResolveOK(t, r, "Show", Con("List", Con("Int")))

	cacheSize := len(r.cache)
	attempts := r.matchAttempts
	count := r.InstanceCount()

	// Rejected AddInstance calls.
	if _, err := r.AddInstance("", Con("Int"), nil); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := r.AddInstance("Show", Con("List", Var(1)), nil); err == nil {
		t.Fatal("expected duplicate rejection")
	}
	if _, err := r.AddInstance("Show", Con("List", Var(8)), nil); err == nil {
		t.Fatal("expected rejection")
	}
	// Rejected Resolve calls.
	if _, _, err := r.Resolve("Show", Con("List", Var(0))); err == nil {
		t.Fatal("expected rejection")
	}
	if _, _, err := r.Resolve("", Con("Int")); err == nil {
		t.Fatal("expected rejection")
	}

	if got := r.InstanceCount(); got != count {
		t.Fatalf("instance count changed: got %d, want %d", got, count)
	}
	if got := len(r.cache); got != cacheSize {
		t.Fatalf("cache size changed: got %d, want %d", got, cacheSize)
	}
	if got := r.matchAttempts; got != attempts {
		t.Fatalf("match attempts changed: got %d, want %d", got, attempts)
	}
	// The next accepted instance gets the next id.
	if id := mustAdd(t, r, "Show", Con("Char")); id != 3 {
		t.Fatalf("instance id: got %d, want 3", id)
	}
}
