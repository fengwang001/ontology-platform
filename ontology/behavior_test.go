package ontology

import "testing"

func TestRejectionsAndStateUnchanged(t *testing.T) {
	for _, params := range [][2]int{{0, 10}, {17, 10}, {1, 0}, {1, 100001}} {
		if _, err := New(params[0], params[1]); err == nil {
			t.Fatalf("New(%d,%d) succeeded", params[0], params[1])
		}
	}
	v, _ := New(2, 2)
	mustAdd(t, v, rootSpec("r"))
	bad := testCert(rootSpec(""))
	requireReject(t, v.Add(bad), RejectInvalidArgument)
	requireReject(t, v.Add(testCert(rootSpec("r"))), RejectConflict)
	requireReject(t, v.Trust(Bytes("missing")), RejectNotFound)
	requireReject(t, v.Revoke(Bytes("missing"), 1), RejectNotFound)
	mustAdd(t, v, certSpec{id: "x", subject: "x", issuer: "root", key: "kx", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: -1})
	requireReject(t, v.Add(testCert(rootSpec("r"))), RejectConflict)
	requireReject(t, v.Add(testCert(rootSpec("z"))), RejectLimitExceeded)
	_, err := v.Verify(Bytes("missing"), "a.com", 1)
	requireReject(t, err, RejectNotFound)
	_, err = v.Verify(Bytes("r"), "bad..com", 1)
	requireReject(t, err, RejectInvalidArgument)
	_, err = v.Verify(Bytes("r"), "a.com", maxTime+1)
	requireReject(t, err, RejectInvalidArgument)

	mutated := testCert(rootSpec("r"))
	mutated.ID = Bytes("changed")
	mutated.NotAfter = 2
	v2, _ := New(2, 2)
	mustAdd(t, v2, certSpec{id: "x", subject: "x", issuer: "root", key: "kx", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v2, certSpec{id: "al", subject: "al", issuer: "x", key: "kal", authKey: "kx", nb: 0, na: 100, san: []string{"a.com"}})
	mustTrust(t, v2, "al")
	result, _ := v2.Verify(Bytes("al"), "a.com", 1)
	assertPath(t, result, "al")
}

func TestValidityAndRevocationBoundaries(t *testing.T) {
	v, _ := New(2, 10)
	mustAdd(t, v, certSpec{id: "l", subject: "leaf", issuer: "root", key: "kl", authKey: "kr", nb: 10, na: 20, san: []string{"a.com"}})
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")

	for _, tc := range []struct {
		now    int64
		reason FailureReason
	}{{9, FailureNotYetValid}, {10, ""}, {19, ""}, {20, FailureExpired}} {
		result, _ := v.Verify(Bytes("l"), "a.com", tc.now)
		if tc.reason == "" {
			assertPath(t, result, "l", "r")
		} else {
			assertFailure(t, result, tc.reason, 0, "l", "r")
		}
	}

	if err := v.Revoke(Bytes("l"), 15); err != nil {
		t.Fatal(err)
	}
	if err := v.Revoke(Bytes("l"), 5); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, v, certSpec{id: "l2", subject: "leaf2", issuer: "root", key: "kl2", authKey: "kr", nb: 0, na: 20, san: []string{"a.com"}})
	if err := v.Revoke(Bytes("l2"), 5); err != nil {
		t.Fatal(err)
	}
	result, _ := v.Verify(Bytes("l2"), "a.com", 5)
	assertFailure(t, result, FailureRevoked, 0, "l2", "r")
	result, _ = v.Verify(Bytes("l2"), "a.com", 4)
	assertPath(t, result, "l2", "r")
	result, _ = v.Verify(Bytes("l"), "a.com", 9)
	assertFailure(t, result, FailureNotYetValid, 0, "l", "r")
}

func TestNonCAAndPathLength(t *testing.T) {
	v, _ := New(4, 30)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "n", subject: "n", issuer: "root", key: "kn", authKey: "kr", nb: 0, na: 100})
	mustAdd(t, v, certSpec{id: "ln", subject: "ln", issuer: "n", key: "kln", authKey: "kn", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ := v.Verify(Bytes("ln"), "a.com", 1)
	assertFailure(t, result, FailureNotCA, 1, "ln", "n", "r")

	mustAdd(t, v, certSpec{id: "i0", subject: "i", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: 0})
	mustAdd(t, v, certSpec{id: "l0", subject: "l0", issuer: "i", key: "k0", authKey: "ki", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ = v.Verify(Bytes("l0"), "a.com", 1)
	assertPath(t, result, "l0", "i0", "r")

	mustAdd(t, v, certSpec{id: "i1", subject: "i1", issuer: "i", key: "k1", authKey: "ki", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "l1", subject: "l1", issuer: "i1", key: "kl1", authKey: "k1", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ = v.Verify(Bytes("l1"), "a.com", 1)
	assertFailure(t, result, FailurePathLenExceeded, 2, "l1", "i1", "i0", "r")

	mustAdd(t, v, certSpec{id: "ar", subject: "ar", issuer: "ar", key: "kar", authKey: "kar", nb: 0, na: 100, ca: true, pathLen: 0})
	mustTrust(t, v, "ar")
	mustAdd(t, v, certSpec{id: "ai", subject: "ai", issuer: "ar", key: "kai", authKey: "kar", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "al", subject: "al", issuer: "ai", key: "kal", authKey: "kai", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ = v.Verify(Bytes("al"), "a.com", 1)
	assertFailure(t, result, FailurePathLenExceeded, 2, "al", "ai", "ar")
}

func TestSelfIssuedPathLength(t *testing.T) {
	v, _ := New(4, 20)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "i0", subject: "i", issuer: "root", key: "ki", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: 0})
	mustAdd(t, v, certSpec{id: "s", subject: "i", issuer: "i", key: "ks", authKey: "ki", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "l", subject: "l", issuer: "i", key: "kl", authKey: "ks", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ := v.Verify(Bytes("l"), "a.com", 1)
	assertPath(t, result, "l", "s", "i0", "r")
}

func TestPathLenOneBoundary(t *testing.T) {
	v, _ := New(5, 30)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "p1", subject: "p1", issuer: "root", key: "kp1", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: 1})
	mustAdd(t, v, certSpec{id: "i1", subject: "i1", issuer: "p1", key: "ki1", authKey: "kp1", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "l1", subject: "l1", issuer: "i1", key: "kl1", authKey: "ki1", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ := v.Verify(Bytes("l1"), "a.com", 1)
	assertPath(t, result, "l1", "i1", "p1", "r")

	mustAdd(t, v, certSpec{id: "i2", subject: "i2", issuer: "i1", key: "ki2", authKey: "ki1", nb: 0, na: 100, ca: true, pathLen: -1})
	mustAdd(t, v, certSpec{id: "l2", subject: "l2", issuer: "i2", key: "kl2", authKey: "ki2", nb: 0, na: 100, san: []string{"a.com"}})
	result, _ = v.Verify(Bytes("l2"), "a.com", 1)
	assertFailure(t, result, FailurePathLenExceeded, 3, "l2", "i2", "i1", "p1", "r")
}

func TestDNSAndSAN(t *testing.T) {
	v, _ := New(3, 20)
	mustAdd(t, v, rootSpec("r"))
	mustTrust(t, v, "r")
	mustAdd(t, v, certSpec{id: "ca", subject: "ca", issuer: "root", key: "kc", authKey: "kr", nb: 0, na: 100, ca: true, pathLen: -1, permitted: []string{".example.com"}, excluded: []string{"b.example.com"}})
	mustAdd(t, v, certSpec{id: "l", subject: "l", issuer: "ca", key: "kl", authKey: "kc", nb: 0, na: 100, san: []string{"*.example.com", "b.example.com"}})
	result, _ := v.Verify(Bytes("l"), "example.com", 1)
	assertFailure(t, result, FailurePermittedDNS, 1, "l", "ca", "r")
	result, _ = v.Verify(Bytes("l"), "notexample.com", 1)
	assertFailure(t, result, FailurePermittedDNS, 1, "l", "ca", "r")
	result, _ = v.Verify(Bytes("l"), "a.example.com", 1)
	assertPath(t, result, "l", "ca", "r")
	result, _ = v.Verify(Bytes("l"), "b.example.com", 1)
	assertFailure(t, result, FailureExcludedDNS, 1, "l", "ca", "r")
	result, _ = v.Verify(Bytes("l"), "a.b.example.com", 1)
	assertFailure(t, result, FailureExcludedDNS, 1, "l", "ca", "r")
	leaf := v.certs["l"].cert
	leaf.SAN = []string{"*.example.com"}
	v.certs["l"].cert = leaf
	result, _ = v.Verify(Bytes("l"), "a.x.example.com", 1)
	assertFailure(t, result, FailureSANMismatch, 0, "l", "ca", "r")
}
