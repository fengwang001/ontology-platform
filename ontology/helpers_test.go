package ontology

import "sort"

func sortRowKeys(in []RowKey) {
	sort.Slice(in, func(i, j int) bool { return in[i].Row < in[j].Row })
}

func sortStrings(in []string) { sort.Strings(in) }

func expectReason(t testingT, got error, want Reason) {
	t.Helper()
	if want == ReasonOK {
		if got != nil {
			t.Fatalf("expected nil error, got %v", got)
		}
		return
	}
	if got == nil {
		t.Fatalf("expected error %s, got nil", reasonString(want))
	}
	te, ok := got.(*Error)
	if !ok {
		t.Fatalf("expected *ontology.Error, got %T", got)
	}
	if te.Reason != want {
		t.Fatalf("expected reason %s, got %s", reasonString(want), reasonString(te.Reason))
	}
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
}

func errorInfo(err error) (Reason, int, string) {
	if err == nil {
		return ReasonOK, -1, ""
	}
	e := err.(*Error)
	return e.Reason, e.OpIndex, e.ViolatedKey
}
