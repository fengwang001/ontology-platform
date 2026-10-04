package gate_test

import (
	"errors"
	"testing"

	"ontology/compat"
	"ontology/gate"
	"ontology/schema"
)

func gf(name string, t schema.Type, required, hasDefault bool) schema.Field {
	return schema.Field{Name: name, Type: t, Required: required, HasDefault: hasDefault}
}

func v1Fields() []schema.Field {
	return []schema.Field{
		gf("id", schema.Int32, true, false),
		gf("name", schema.String, true, false),
		gf("age", schema.Int32, false, false),
	}
}

func candX() []schema.Field {
	return []schema.Field{
		gf("id", schema.Int64, true, false),
		gf("name", schema.String, true, false),
		gf("email", schema.String, true, true),
	}
}

func candY() []schema.Field {
	return []schema.Field{
		gf("id", schema.Int32, true, false),
		gf("name", schema.String, true, false),
		gf("email", schema.String, true, true),
	}
}

func setupSubject(t *testing.T, mode schema.Mode) *gate.Registry {
	t.Helper()
	r := gate.NewRegistry()
	if v, err := r.CreateSubject("s", mode, v1Fields(), 0); err != nil || v != 1 {
		t.Fatalf("CreateSubject = %d,%v", v, err)
	}
	return r
}

func blockersEqual(got, want []gate.Blocker) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestSpecExampleBackward(t *testing.T) {
	r := setupSubject(t, schema.Backward)
	if err := r.Subscribe("s", "c1", 1, []string{"id", "name"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Subscribe("s", "c2", 1, []string{"id", "age"}, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Publish("s", candX(), 1); !errors.Is(err, gate.ErrBlocked) {
		t.Fatalf("X: want ErrBlocked, got %v", err)
	} else {
		want := []gate.Blocker{
			{Consumer: "c1", Violation: compat.Violation{Field: "id", Reason: compat.TypeMismatch}},
			{Consumer: "c2", Violation: compat.Violation{Field: "id", Reason: compat.TypeMismatch}},
		}
		if !blockersEqual(err.(*gate.BlockedError).Blockers, want) {
			t.Fatalf("X blockers = %+v, want %+v", err.(*gate.BlockedError).Blockers, want)
		}
	}

	_, err := r.Publish("s", candY(), 1)
	if !errors.Is(err, gate.ErrBlocked) {
		t.Fatalf("Y: want ErrBlocked, got %v", err)
	}
	want := []gate.Blocker{{Consumer: "c2", Violation: compat.Violation{Field: "age", Reason: compat.MissingNoDefault}}}
	if !blockersEqual(err.(*gate.BlockedError).Blockers, want) {
		t.Fatalf("Y blockers = %+v", err.(*gate.BlockedError).Blockers)
	}

	if err := r.Waive("s", "c2", 50, 2); err != nil {
		t.Fatal(err)
	}
	v, err := r.Publish("s", candY(), 49)
	if err != nil || v != 2 {
		t.Fatalf("Y@49 = %d,%v", v, err)
	}
	st, err := r.Status("s", "c2")
	if err != nil || st != (gate.StatusInfo{Pinned: 1, Lagging: true, LaggedAtVer: 2}) {
		t.Fatalf("c2 status = %+v,%v", st, err)
	}

	if _, err := r.Publish("s", candX(), 50); !errors.Is(err, gate.ErrBlocked) {
		t.Fatalf("X@50: waiver expired, want ErrBlocked, got %v", err)
	}

	if err := r.Advance("s", "c2", 2, []string{"id", "email"}, 50); err != nil {
		t.Fatalf("Advance c2: %v", err)
	}
	st, _ = r.Status("s", "c2")
	if st != (gate.StatusInfo{Pinned: 2, Lagging: false, LaggedAtVer: 0}) {
		t.Fatalf("after advance c2 = %+v", st)
	}
}

func TestWaiverBoundary(t *testing.T) {
	for _, tc := range []struct {
		now     int64
		allowed bool
	}{{48, true}, {49, true}, {50, false}} {
		r := gate.NewRegistry()
		_, _ = r.CreateSubject("s", schema.Backward, v1Fields(), 0)
		if err := r.Subscribe("s", "c2", 1, []string{"id", "age"}, 0); err != nil {
			t.Fatal(err)
		}
		if err := r.Waive("s", "c2", 50, 0); err != nil {
			t.Fatal(err)
		}
		_, err := r.Publish("s", candY(), tc.now)
		if tc.allowed && err != nil {
			t.Fatalf("now=%d: expected allow, got %v", tc.now, err)
		}
		if !tc.allowed && !errors.Is(err, gate.ErrBlocked) {
			t.Fatalf("now=%d: want block, got %v", tc.now, err)
		}
	}
	r := gate.NewRegistry()
	_, _ = r.CreateSubject("s", schema.Backward, v1Fields(), 0)
	_ = r.Subscribe("s", "c2", 1, []string{"id", "age"}, 0)
	if err := r.Waive("s", "c2", 5, 10); !errors.Is(err, gate.ErrInvalidArgument) {
		t.Fatalf("until<=now: want invalid, got %v", err)
	}
}

func TestLaggingStopsBlocking(t *testing.T) {
	r := setupSubject(t, schema.Backward)
	if err := r.Subscribe("s", "c2", 1, []string{"id", "age"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Waive("s", "c2", 50, 0); err != nil {
		t.Fatal(err)
	}
	if v, err := r.Publish("s", candY(), 10); err != nil || v != 2 {
		t.Fatalf("first publish = %d,%v", v, err)
	}
	z := []schema.Field{
		gf("id", schema.Int32, true, false),
		gf("name", schema.String, true, false),
		gf("email", schema.String, true, true),
		gf("phone", schema.String, true, true),
	}
	if v, err := r.Publish("s", z, 50); err != nil || v != 3 {
		t.Fatalf("publish after lagging = %d,%v", v, err)
	}
}

func TestAdvanceFailureAtomic(t *testing.T) {
	r := setupSubject(t, schema.Backward)
	if err := r.Subscribe("s", "c2", 1, []string{"id", "age"}, 0); err != nil {
		t.Fatal(err)
	}
	_ = r.Waive("s", "c2", 50, 0)
	if _, err := r.Publish("s", candY(), 10); err != nil {
		t.Fatal(err)
	}
	before, _ := r.Status("s", "c2")
	if err := r.Advance("s", "c2", 1, []string{"id", "email"}, 11); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("to<=pinned: want not found, got %v", err)
	}
	if err := r.Advance("s", "c2", 2, []string{"id", "age"}, 11); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("field absent from target version: want not found, got %v", err)
	}
	// Move latest to v3: name string -> bytes is BACKWARD-compatible
	// (string->bytes promotes) but a v2-pinned view with name cannot read it.
	v3 := []schema.Field{
		gf("id", schema.Int32, true, false),
		gf("name", schema.Bytes, true, false),
		gf("email", schema.String, true, true),
	}
	if _, err := r.Publish("s", v3, 11); err != nil {
		t.Fatalf("publish v3: %v", err)
	}
	if err := r.Advance("s", "c2", 2, []string{"id", "name"}, 12); !errors.Is(err, gate.ErrStillBroken) {
		t.Fatalf("new view cannot read latest: want still broken, got %v", err)
	}
	after, _ := r.Status("s", "c2")
	if after != before {
		t.Fatalf("state changed on failed advance: before=%+v after=%+v", before, after)
	}
}

func TestDeleteUnsubscribedField(t *testing.T) {
	r := setupSubject(t, schema.Backward)
	if err := r.Subscribe("s", "c1", 1, []string{"id", "name"}, 0); err != nil {
		t.Fatal(err)
	}
	n := []schema.Field{gf("id", schema.Int32, true, false), gf("name", schema.String, true, false)}
	if v, err := r.Publish("s", n, 1); err != nil || v != 2 {
		t.Fatalf("delete age = %d,%v", v, err)
	}
}

func TestModeOppositeConclusions(t *testing.T) {
	rb := setupSubject(t, schema.Backward)
	if err := rb.Subscribe("s", "c1", 1, []string{"id", "name", "age"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, errB := rb.Publish("s", candY(), 1); errors.Is(errB, gate.ErrIncompatible) {
		t.Fatalf("BACKWARD must not flag Y incompatible, got %v", errB)
	}

	rf := setupSubject(t, schema.Forward)
	if err := rf.Subscribe("s", "c1", 1, []string{"id", "name"}, 0); err != nil {
		t.Fatal(err)
	}
	_, errF := rf.Publish("s", candY(), 1)
	var ie *gate.IncompatibleError
	if !errors.As(errF, &ie) {
		t.Fatalf("FORWARD want IncompatibleError, got %v", errF)
	}
	if ie.Direction != compat.Forward || ie.Violation.Field != "age" || ie.Violation.Reason != compat.MissingNoDefault {
		t.Fatalf("FORWARD violation = %+v", ie)
	}
}

func TestNoChangeBeforeCompat(t *testing.T) {
	r := setupSubject(t, schema.Forward)
	_ = r.Subscribe("s", "c1", 1, []string{"id", "name"}, 0)
	_, err := r.Publish("s", v1Fields(), 1)
	if !errors.Is(err, gate.ErrNoChange) {
		t.Fatalf("want ErrNoChange, got %v", err)
	}
}

func TestIncompatibleBeforeBlocked(t *testing.T) {
	r := setupSubject(t, schema.Forward)
	_ = r.Subscribe("s", "c1", 1, []string{"id", "age"}, 0)
	_, err := r.Publish("s", candY(), 1)
	if !errors.Is(err, gate.ErrIncompatible) {
		t.Fatalf("want ErrIncompatible before subscriber check, got %v", err)
	}
}
