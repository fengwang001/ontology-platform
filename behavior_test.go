package ontology

import "testing"

func TestBehaviorSkeleton(t *testing.T) {}

func verifyCaveat(t *testing.T, caveat string, request Request, now uint64, want bool) {
	t.Helper()
	parsed := parseCaveat([]byte(caveat))
	if got := caveatSatisfied(parsed, request, now); got != want {
		t.Fatalf("%s at %d = %v, want %v", caveat, now, got, want)
	}
}

func TestResourceAndCaveatBoundaries(t *testing.T) {
	request := Request{Op: "read", Res: "/a", Amount: 5}
	verifyCaveat(t, "res:/a", request, 0, true)
	request.Res = "/a/b"
	verifyCaveat(t, "res:/a", request, 0, true)
	request.Res = "/ab"
	verifyCaveat(t, "res:/a", request, 0, false)
	for _, path := range []string{"/", "/anything", "/a/b/c"} {
		verifyCaveat(t, "res:/", Request{Res: path}, 0, true)
	}

	verifyCaveat(t, "exp:10", Request{}, 9, true)
	verifyCaveat(t, "exp:10", Request{}, 10, false)
	verifyCaveat(t, "amt:5", Request{Amount: 5}, 0, true)
	verifyCaveat(t, "amt:4", Request{Amount: 5}, 0, false)

	valid := map[string]bool{
		"exp:0": true, "exp:1000000000000000": true, "exp:01": false, "exp:1000000000000001": false,
		"ops:a": true, "ops:a,b-1,c2": true, "ops:": false, "ops:a,": false, "ops:A": false,
		"res:/": true, "res:/a": true, "res:/a/b": true, "res:/a/": false, "res:/a//b": false, "res:a": false,
		"amt:0": true, "amt:1000000000000": true, "amt:1000000000001": false, "amt:00": false,
		"other:x": false,
	}
	for caveat, want := range valid {
		parsed := parseCaveat([]byte(caveat))
		got := parsed.known && parsed.valid
		if got != want {
			t.Fatalf("%s valid = %v, want %v", caveat, got, want)
		}
	}
}

func TestVerifyCaveatOrderAndKinds(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 8, 64, 8)
	token := testToken("id", "ops:read", "mystery:x", "ops:write")
	err := service.Verify(token, Request{Op: "read", Res: "/", Amount: 0}, 0)
	assertCaveatError(t, err, 2, CaveatUnknown)

	service, _ = NewService([]byte("k"), testMAC, 8, 64, 8)
	token = testToken("id2", "ops:read", "exp:bad", "ops:write")
	err = service.Verify(token, Request{Op: "read", Res: "/", Amount: 0}, 1)
	assertCaveatError(t, err, 2, CaveatMalformed)

	service, _ = NewService([]byte("k"), testMAC, 8, 64, 8)
	token, err = service.Mint([]byte("id3"), [][]byte{
		[]byte("ops:read"),
		[]byte("amt:1"),
		[]byte("ops:write"),
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	err = service.Verify(token, Request{Op: "write", Res: "/", Amount: 2}, 2)
	assertCaveatError(t, err, 1, CaveatUnsatisfied)
}

func testToken(id string, caveats ...string) *Token {
	signature := testMAC([]byte("k"), []byte(id))
	caveatBytes := make([][]byte, 0, len(caveats))
	for _, caveat := range caveats {
		caveatBytes = append(caveatBytes, []byte(caveat))
		signature = testMAC(signature, []byte(caveat))
	}
	return &Token{ID: []byte(id), Caveats: caveatBytes, Sig: signature}
}

func TestMultipleExpiryCaveatsIndependent(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 4, 64, 4)
	token, err := service.Mint([]byte("id"), [][]byte{[]byte("exp:10"), []byte("exp:5")}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Verify(token, Request{Op: "a", Res: "/"}, 4); err != nil {
		t.Fatal(err)
	}
	if err := service.Verify(token, Request{Op: "a", Res: "/"}, 5); err == nil {
		t.Fatal("expiry at strict boundary failed")
	}
}

func assertCaveatError(t *testing.T, err error, index int, kind CaveatKind) {
	t.Helper()
	macErr, ok := err.(*Error)
	if !ok || macErr.Code != ErrCaveat || macErr.Index != index || macErr.Kind != kind {
		t.Fatalf("err=%#v, want caveat %d %s", err, index, kind)
	}
}

func TestPrefixRevocationScopeAndPriority(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 8, 64, 8)
	root, _ := service.Mint([]byte("tree"), nil, 0)
	one, _ := Attenuate(service, root, []byte("ops:read"))
	twoA, _ := Attenuate(service, one, []byte("exp:100"))
	twoB, _ := Attenuate(service, one, []byte("res:/a"))

	result, err := service.Revoke(twoA, 2, 10)
	if err != nil || result.Expired {
		t.Fatalf("revoke result=%#v err=%v", result, err)
	}
	assertVerifyCode(t, service.Verify(twoA, Request{Op: "read", Res: "/x"}, 99), ErrRevoked, 2)
	assertVerifyCode(t, service.Verify(one, Request{Op: "read", Res: "/x"}, 99), "", 0)
	assertVerifyCode(t, service.Verify(twoB, Request{Op: "read", Res: "/x"}, 99), ErrCaveat, 2)

	result, err = service.Revoke(twoA, 1, 99)
	if err != nil || result.Expired {
		t.Fatal(err)
	}
	assertVerifyCode(t, service.Verify(one, Request{Op: "read", Res: "/x"}, 99), ErrRevoked, 1)
	assertVerifyCode(t, service.Verify(twoA, Request{Op: "read", Res: "/x"}, 99), ErrRevoked, 1)
	assertVerifyCode(t, service.Verify(twoB, Request{Op: "read", Res: "/x"}, 99), ErrRevoked, 1)
	assertVerifyCode(t, service.Verify(root, Request{Op: "read", Res: "/x"}, 99), "", 0)

	assertVerifyCode(t, service.Verify(twoA, Request{Op: "read", Res: "/x"}, 100), ErrRevoked, 1)
	result, err = service.Revoke(twoA, 2, 100)
	if err != nil || !result.Expired {
		t.Fatalf("expired result=%#v err=%v", result, err)
	}
}

func TestSpecExpiryExample(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 4, 64, 4)
	t0, _ := service.Mint([]byte("t1"), nil, 0)
	t1, _ := Attenuate(service, t0, []byte("ops:read,list"))
	t2, _ := Attenuate(service, t1, []byte("exp:100"))
	t2b, _ := Attenuate(service, t1, []byte("res:/a"))

	result, err := service.Revoke(t2, 2, 10)
	if err != nil || result.Expired {
		t.Fatal(err)
	}
	assertVerifyCode(t, service.Verify(t2, Request{Op: "read", Res: "/x"}, 99), ErrRevoked, 2)
	assertVerifyCode(t, service.Verify(t2b, Request{Op: "read", Res: "/x"}, 99), ErrCaveat, 2)
	assertVerifyCode(t, service.Verify(t2, Request{Op: "read", Res: "/x"}, 100), ErrCaveat, 2)

	result, err = service.Revoke(t2, 2, 100)
	if err != nil || !result.Expired {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if err := service.Verify(t0, Request{Op: "read", Res: "/x"}, 15); err == nil ||
		err.(*Error).Code != ErrClockRewound {
		t.Fatalf("err=%v", err)
	}
}

func TestRevocationBoundaryUsesMinimumValidExpiry(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 8, 64, 8)
	token := testToken("b", "exp:bad", "exp:5", "exp:10")
	result, err := service.Revoke(token, 3, 4)
	if err != nil || result.Expired {
		t.Fatal(err)
	}
	assertVerifyCode(t, service.Verify(token, Request{Op: "x", Res: "/"}, 4), ErrRevoked, 3)
	assertVerifyCode(t, service.Verify(token, Request{Op: "x", Res: "/"}, 5), ErrCaveat, 1)

	token = testToken("b2", "exp:7")
	result, err = service.Revoke(token, 1, 7)
	if err != nil || !result.Expired {
		t.Fatalf("boundary result=%#v err=%v", result, err)
	}
}

func TestRevocationRequiresRealSignature(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 8, 64, 8)
	token, _ := service.Mint([]byte("id"), nil, 0)
	token.Sig[0] ^= 0xff
	_, err := service.Revoke(token, 0, 0)
	if err == nil || err.(*Error).Code != ErrBadSignature {
		t.Fatalf("err=%v", err)
	}
}

func assertVerifyCode(t *testing.T, err error, code ErrorCode, revokedAt int) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatalf("err=%v, want success", err)
		}
		return
	}
	macErr, ok := err.(*Error)
	if !ok || macErr.Code != code || (code == ErrRevoked && macErr.RevokedAt != revokedAt) {
		t.Fatalf("err=%#v, want %s at %d", err, code, revokedAt)
	}
}
