package contacttracing

import "testing"

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func errKind(err error) ErrKind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return 0
}

func findEntry(entries []ContactEntry, patient string) (ContactEntry, bool) {
	for _, e := range entries {
		if e.Patient == patient {
			return e, true
		}
	}
	return ContactEntry{}, false
}
