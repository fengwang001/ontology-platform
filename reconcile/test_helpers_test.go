package reconcile

import "testing"

func addb(t *testing.T, m *Matcher, id string, amt, day int64, ref string) {
	t.Helper()
	if err := m.AddLine(Bank, id, amt, day, ref); err != nil {
		t.Fatalf("AddLine bank %s: %v", id, err)
	}
}

func addk(t *testing.T, m *Matcher, id string, amt, day int64, ref string) {
	t.Helper()
	if err := m.AddLine(Book, id, amt, day, ref); err != nil {
		t.Fatalf("AddLine book %s: %v", id, err)
	}
}

func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
}

func eqSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertMatches(t *testing.T, got, want []Match) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Mid != want[i].Mid || got[i].Round != want[i].Round ||
			!eqSlice(got[i].BankID, want[i].BankID) || !eqSlice(got[i].BookID, want[i].BookID) {
			t.Fatalf("at %d got %+v want %+v", i, got[i], want[i])
		}
	}
}

func unmatchedErr(t *testing.T, m *Matcher, side Side) []Line {
	t.Helper()
	out, err := m.Unmatched(side)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
