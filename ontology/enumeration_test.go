package ontology

import "testing"

func buildCrossSignedExample(t *testing.T, limit int) *Validator {
	t.Helper()
	v, _ := New(limit, 20)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "i1", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 500, ca: true, pathLen: 0, excluded: []string{"example.com"}})
	mustAdd(t, v, certSpec{id: "i2", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 900, ca: true, pathLen: 0})
	mustAdd(t, v, certSpec{id: "leaf", subject: "leaf", issuer: "int", key: "kl", authKey: "ki", nb: 0, na: 400, san: []string{"*.example.com"}})
	return v
}

func TestCandidateOrderBacktrackingCountAndFirstFailure(t *testing.T) {
	v := buildCrossSignedExample(t, 3)
	result, err := v.Verify(Bytes("leaf"), "a.example.com", 100)
	if err != nil {
		t.Fatal(err)
	}
	assertPath(t, result, "leaf", "i2", "r")
	if result.ExaminedCandidates() != 2 {
		t.Fatalf("count=%d, want 2", result.ExaminedCandidates())
	}
	if err := v.Revoke(Bytes("i2"), 50); err != nil {
		t.Fatal(err)
	}
	result, _ = v.Verify(Bytes("leaf"), "a.example.com", 100)
	assertFailure(t, result, FailureRevoked, 1, "leaf", "i2", "r")
	if result.NoPath {
		t.Fatal("failed structural paths exist; NoPath must be false")
	}
	if result.ExaminedCandidates() != 4 {
		t.Fatalf("count=%d, want 4", result.ExaminedCandidates())
	}
}

func TestCandidateNotAfterTieByIDAndUnrelatedIndexCount(t *testing.T) {
	v, _ := New(3, 11000)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "b", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 500, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "a", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 500, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "leaf", subject: "leaf", issuer: "int", key: "kl", authKey: "ki", nb: 0, na: 500, san: []string{"a.com"}})
	result, _ := v.Verify(Bytes("leaf"), "a.com", 1)
	assertPath(t, result, "leaf", "a", "r")
	before := result.ExaminedCandidates()
	for idx := 0; idx < 10000; idx++ {
		id := "unrelated-" + string(rune('a'+idx%26)) + "-" + string(rune('0'+idx/26%10)) + string(rune('0'+idx/260%10)) + string(rune('0'+idx/2600))
		mustAdd(t, v, certSpec{id: id, subject: id, issuer: id, key: id, authKey: id, nb: 0, na: 500, ca: true, pathLen: -1})
	}
	result, _ = v.Verify(Bytes("leaf"), "a.com", 1)
	assertPath(t, result, "leaf", "a", "r")
	if result.ExaminedCandidates() != before {
		t.Fatalf("count changed after unrelated registrations: before=%d after=%d", before, result.ExaminedCandidates())
	}
}

func TestBacktrackingToSecondCandidatePassesAndStops(t *testing.T) {
	v, _ := New(4, 20)
	mustAdd(t, v, certSpec{id: "r", subject: "root", issuer: "root", key: "kr", authKey: "kr", nb: 0, na: 1000, ca: true, pathLen: -1, san: []string{"other.com"}})
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "bad", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 900, ca: true, pathLen: -1, permitted: []string{"z.com"}})
	mustAdd(t, v, certSpec{id: "good", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 500, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "leaf", subject: "leaf", issuer: "int", key: "kl", authKey: "ki", nb: 0, na: 500, san: []string{"a.example.com", "other.com"}})
	result, err := v.Verify(Bytes("leaf"), "other.com", 1)
	if err != nil {
		t.Fatal(err)
	}
	assertPath(t, result, "leaf", "good", "r")
	if result.ExaminedCandidates() != 4 {
		t.Fatalf("count=%d, want bad,r,good,r", result.ExaminedCandidates())
	}
}

func TestCrossSignedCycleDoesNotLoopAndLengthLimit(t *testing.T) {
	v, _ := New(5, 20)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "a", subject: "ca", issuer: "root", key: "ka", authKey: "kr", nb: 0, na: 50, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "b", subject: "cb", issuer: "ca", key: "kb", authKey: "ka", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "x", subject: "ca", issuer: "cb", key: "ka", authKey: "kb", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "leaf", subject: "leaf", issuer: "ca", key: "kl", authKey: "ka", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ := v.Verify(Bytes("leaf"), "a.com", 1)
	assertPath(t, result, "leaf", "x", "b", "a", "r")

	short, _ := New(2, 20)
	mustAdd(t, short, rootSpec("r"))
	mustTrust(t, short, "r")
	for _, spec := range []certSpec{
		{id: "a", subject: "ca", issuer: "root", key: "ka", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: -1},
		{id: "b", subject: "cb", issuer: "ca", key: "kb", authKey: "ka", nb: 0, na: 100, ca: true, pathLen: -1},
		{id: "x", subject: "ca", issuer: "cb", key: "ka", authKey: "kb", nb: 0, na: 100, ca: true, pathLen: -1},
		{id: "leaf", subject: "leaf", issuer: "ca", key: "kl", authKey: "ka", nb: 0, na: 100, san: []string{"a.com"}},
	} {
		mustAdd(t, short, spec)
	}
	result, _ = short.Verify(Bytes("leaf"), "a.com", 1)
	if !result.NoPath || result.Failure != nil {
		t.Fatalf("got noPath=%v failure=%#v, want no structural path", result.NoPath, result.Failure)
	}
}

func TestLeafTrustStopsImmediately(t *testing.T) {
	v, _ := New(3, 10)
	mustAdd(t, v, certSpec{id: "leaf", subject: "leaf", issuer: "missing", key: "kl", authKey: "km", nb: 0, na: 100, ca: false, san: []string{"a.com"}})
	mustTrust(t, v, "leaf")
	result, err := v.Verify(Bytes("leaf"), "a.com", 1)
	if err != nil {
		t.Fatal(err)
	}
	assertPath(t, result, "leaf")
	if result.ExaminedCandidates() != 0 {
		t.Fatalf("count=%d, want 0", result.ExaminedCandidates())
	}
}

func TestLengthLimitCandidatePruneCount(t *testing.T) {
	v, _ := New(3, 20)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "i", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "leaf", subject: "leaf", issuer: "int", key: "kl", authKey: "ki", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ := v.Verify(Bytes("leaf"), "a.com", 1)
	assertPath(t, result, "leaf", "i", "r")

	short, _ := New(2, 20)
	mustAdd(t, short, rootSpec("r"))
	mustTrust(t, short, "r")
	mustAdd(t, short, certSpec{id: "i", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, short, certSpec{id: "x", subject: "int", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 50, ca: true, pathLen: -1})
	mustAdd(t, short, certSpec{id: "leaf", subject: "leaf", issuer: "int", key: "kl", authKey: "ki", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ = short.Verify(Bytes("leaf"), "a.com", 1)
	if !result.NoPath {
		t.Fatalf("path=%v failure=%#v, want pruned branches", result.Path, result.Failure)
	}
	if result.ExaminedCandidates() != 4 {
		t.Fatalf("count=%d, want both length-pruned candidates examined from i and x", result.ExaminedCandidates())
	}
}
