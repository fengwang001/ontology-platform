package ontology

import (
	"errors"
	"testing"
)

type certSpec struct {
	id        string
	subject   string
	issuer    string
	key       string
	authKey   string
	nb        int64
	na        int64
	ca        bool
	pathLen   int
	permitted []string
	excluded  []string
	san       []string
}

func testCert(s certSpec) Certificate {
	pathLen := -1
	if s.ca {
		pathLen = s.pathLen
	}
	return Certificate{
		ID: Bytes(s.id), Subject: Bytes(s.subject), Issuer: Bytes(s.issuer),
		Key: Bytes(s.key), AuthKey: Bytes(s.authKey),
		NotBefore: s.nb, NotAfter: s.na, IsCA: s.ca, PathLen: pathLen,
		Permitted: s.permitted, Excluded: s.excluded, SAN: s.san,
	}
}

func mustAdd(t *testing.T, v *Validator, spec certSpec) {
	t.Helper()
	if err := v.Add(testCert(spec)); err != nil {
		t.Fatalf("Add(%s): %v", spec.id, err)
	}
}

func mustTrust(t *testing.T, v *Validator, id string) {
	t.Helper()
	if err := v.Trust(Bytes(id)); err != nil {
		t.Fatalf("Trust(%s): %v", id, err)
	}
}

func requireReject(t *testing.T, err error, kind RejectKind) {
	t.Helper()
	var reject *RejectError
	if !errors.As(err, &reject) || reject.Kind != kind {
		t.Fatalf("got %v, want %s", err, kind)
	}
}

func assertPath(t *testing.T, result VerifyResult, want ...string) {
	t.Helper()
	if result.NoPath || result.Failure != nil {
		t.Fatalf("got noPath=%v failure=%#v, want %v", result.NoPath, result.Failure, want)
	}
	assertIDs(t, result.Path, want)
}

func assertIDs(t *testing.T, got []Bytes, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %v (%v)", len(got), want, got)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func assertFailure(t *testing.T, result VerifyResult, reason FailureReason, idx int, path ...string) {
	t.Helper()
	if result.NoPath || result.Failure == nil {
		t.Fatalf("got noPath=%v failure=%v", result.NoPath, result.Failure)
	}
	if result.Failure.Reason != reason || result.Failure.CertIdx != idx {
		t.Fatalf("failure=%#v, want %s at %d", result.Failure, reason, idx)
	}
	assertIDs(t, result.Failure.Path, path)
}

func rootSpec(id string) certSpec {
	return certSpec{id: id, subject: "root", issuer: "root", key: "kr", authKey: "kr", nb: 0, na: 1000, ca: true, pathLen: -1}
}
