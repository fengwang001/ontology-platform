package ontology

import (
	"errors"
	"testing"
)

func checkValidation(t *testing.T, err error, field string, kind ValidationKind) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	if ve.Field != field || ve.Kind != kind {
		t.Fatalf("error = %+v, want field %q kind %v", ve, field, kind)
	}
}

func TestUpdateUnknownField(t *testing.T) {
	m := newTestManager(t)
	before := m.CurrentVersion()
	_, err := m.Update(map[string]any{"ghost": 1})
	checkValidation(t, err, "ghost", KindUnknownField)
	if m.CurrentVersion() != before {
		t.Fatalf("version advanced to %d on failed update", m.CurrentVersion())
	}
}

func TestUpdateTypeMismatch(t *testing.T) {
	m := newTestManager(t)
	before := m.CurrentVersion()
	if _, err := m.Update(map[string]any{"port": "8080"}); true {
		checkValidation(t, err, "port", KindTypeMismatch)
	}
	if _, err := m.Update(map[string]any{"host": 42}); true {
		checkValidation(t, err, "host", KindTypeMismatch)
	}
	if m.CurrentVersion() != before {
		t.Fatalf("version advanced to %d on failed update", m.CurrentVersion())
	}
}

func TestUpdateOutOfRange(t *testing.T) {
	m := newTestManager(t)
	before := m.CurrentVersion()
	_, err := m.Update(map[string]any{"port": 70000})
	checkValidation(t, err, "port", KindOutOfRange)
	_, err = m.Update(map[string]any{"retries": -1})
	checkValidation(t, err, "retries", KindOutOfRange)
	if m.CurrentVersion() != before {
		t.Fatalf("version advanced to %d on failed update", m.CurrentVersion())
	}
}

func TestUpdateIsAtomic(t *testing.T) {
	m := newTestManager(t)
	before := m.CurrentVersion()
	// One valid change plus one invalid change: nothing may apply.
	_, err := m.Update(map[string]any{"host": "b", "port": 0})
	checkValidation(t, err, "port", KindOutOfRange)
	if m.CurrentVersion() != before {
		t.Fatalf("version advanced to %d on failed update", m.CurrentVersion())
	}
	snap := m.Acquire()
	defer snap.Release()
	if got := mustGet(t, snap, "host"); got != "a" {
		t.Fatalf("host = %v after rejected update, want a", got)
	}
}

func TestNewManagerValidatesInitial(t *testing.T) {
	if _, err := NewManager(testDecls(), map[string]any{"host": "a", "port": 80}); err == nil {
		t.Fatal("missing field in initial config should fail")
	}
	bad := map[string]any{"host": "a", "port": 80, "retries": 3, "extra": 1}
	if _, err := NewManager(testDecls(), bad); err == nil {
		t.Fatal("unknown field in initial config should fail")
	}
	bad = map[string]any{"host": "a", "port": 0, "retries": 3}
	var ve *ValidationError
	if _, err := NewManager(testDecls(), bad); !errors.As(err, &ve) || ve.Kind != KindOutOfRange {
		t.Fatalf("out-of-range initial = %v, want ValidationError/out of range", err)
	}
}

func TestIntAcceptedAsIntAndInt64(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Update(map[string]any{"port": int64(8080)}); err != nil {
		t.Fatalf("int64 update: %v", err)
	}
	snap := m.Acquire()
	defer snap.Release()
	if got := mustGet(t, snap, "port"); got != int64(8080) {
		t.Fatalf("port = %v (%T), want int64(8080)", got, got)
	}
}

func TestSameValueUpdateKeepsSourceVersion(t *testing.T) {
	m := newTestManager(t)
	v2 := mustUpdate(t, m, map[string]any{"host": "b"})
	v3 := mustUpdate(t, m, map[string]any{"host": "b"}) // identical value
	if v3 <= v2 {
		t.Fatalf("version did not advance: %d -> %d", v2, v3)
	}
	snap := m.Acquire()
	defer snap.Release()
	got, err := snap.SourceVersion("host")
	if err != nil {
		t.Fatal(err)
	}
	if got != v2 {
		t.Fatalf("source = %d, want %d: same value must not advance source", got, v2)
	}
	v4 := mustUpdate(t, m, map[string]any{"host": "c"})
	snap2 := m.Acquire()
	defer snap2.Release()
	got, _ = snap2.SourceVersion("host")
	if got != v4 {
		t.Fatalf("source = %d, want %d after real change", got, v4)
	}
}
