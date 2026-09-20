package ontology

import "testing"

// rec builds a property map from alternating column/value pairs.
func rec(pairs ...string) map[string]Value {
	if len(pairs)%2 != 0 {
		panic("rec: odd number of arguments")
	}
	m := make(map[string]Value, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = Str(pairs[i+1])
	}
	return m
}

// nullRec builds a property map with a single NULL column.
func nullRec(col string) map[string]Value {
	return map[string]Value{col: Null()}
}

// mustConflict inserts and requires a *ConflictError.
func mustConflict(t *testing.T, err error) *ConflictError {
	t.Helper()
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("expected *ConflictError, got %v (%T)", err, err)
	}
	return ce
}

// mustNoErr fails the test if err is non-nil.
func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
