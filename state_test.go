package ontology

import (
	"testing"
)

func TestClockAdvanceAndRejectNoState(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 4, 64, 4)
	token, _ := service.Mint([]byte("id"), nil, 10)

	err := service.Verify(token, Request{Op: "a", Res: "relative"}, 11)
	if err == nil || err.(*Error).Code != ErrInvalid {
		t.Fatal(err)
	}

	err = service.Verify(token, Request{Op: "a", Res: "/"}, 9)
	if err == nil || err.(*Error).Code != ErrClockRewound {
		t.Fatalf("err=%v", err)
	}

	badSig := *token
	badSig.Sig = append([]byte(nil), token.Sig...)
	badSig.Sig[0] ^= 0xff
	if err := service.Verify(&badSig, Request{Op: "a", Res: "/"}, 11); err == nil ||
		err.(*Error).Code != ErrBadSignature {
		t.Fatalf("bad signature err=%v", err)
	}
	if service.Clock() != 11 {
		t.Fatalf("accepted bad-signature Verify should advance clock, got %d", service.Clock())
	}
	if err := service.Verify(token, Request{Op: "a", Res: "/"}, 10); err == nil ||
		err.(*Error).Code != ErrClockRewound {
		t.Fatalf("err=%v", err)
	}
}

func TestRevocationLimitCountsAfterExpiry(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 4, 64, 1)
	tokenA := testToken("a", "exp:10")
	tokenB := testToken("b")

	if _, err := service.Revoke(tokenA, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(tokenB, 0, 5); err == nil || err.(*Error).Code != ErrLimit {
		t.Fatalf("err=%v", err)
	}
	result, err := service.Revoke(tokenB, 0, 10)
	if err != nil || result.Expired {
		t.Fatalf("after expiry result=%#v err=%v", result, err)
	}
}

func TestAttenuateLimitAndMACCalls(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 1, 4, 4)
	token, _ := service.Mint([]byte("id"), nil, 0)
	full, err := Attenuate(service, token, []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Attenuate(service, &Token{ID: []byte("id"), Caveats: [][]byte{{1}, {2}}, Sig: []byte("s")},
		[]byte("b"))
	if err == nil || err.(*Error).Code != ErrInvalid {
		t.Fatalf("invalid old shape err=%v", err)
	}
	before := service.MACCalls()
	_, err = Attenuate(service, token, nil)
	if err == nil || err.(*Error).Code != ErrInvalid {
		t.Fatalf("argument err=%v", err)
	}
	if service.MACCalls() != before {
		t.Fatal("invalid Attenuate invoked MAC")
	}

	_, err = Attenuate(service, full, []byte("b"))
	if err == nil || err.(*Error).Code != ErrLimit {
		t.Fatalf("limit err=%v", err)
	}
}

func TestLookupStopsAtFirstRevocation(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 4, 64, 4)
	token := testToken("id", "ops:a", "exp:100")
	if _, err := service.Revoke(token, 1, 0); err != nil {
		t.Fatal(err)
	}
	before := service.LookupCalls()
	err := service.Verify(token, Request{Op: "a", Res: "/"}, 1)
	if err == nil || err.(*Error).Code != ErrRevoked || err.(*Error).RevokedAt != 1 {
		t.Fatalf("err=%v", err)
	}
	if calls := service.LookupCalls() - before; calls != 2 {
		t.Fatalf("lookup calls=%d, want 2", calls)
	}
}

func TestHeapPopBoundedByExpiredRecords(t *testing.T) {
	service, _ := NewService([]byte("k"), testMAC, 8, 64, 8)
	tokenA := testToken("a", "exp:10")
	tokenB := testToken("b", "exp:20")
	tokenC := testToken("c")
	if _, err := service.Revoke(tokenA, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(tokenB, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Revoke(tokenC, 0, 0); err != nil {
		t.Fatal(err)
	}
	before := service.HeapPopCalls()
	_ = service.Verify(tokenC, Request{Op: "a", Res: "/"}, 15)
	if popped := service.HeapPopCalls() - before; popped != 1 {
		t.Fatalf("popped=%d, want 1", popped)
	}
	before = service.HeapPopCalls()
	_ = service.Verify(tokenC, Request{Op: "a", Res: "/"}, 25)
	if popped := service.HeapPopCalls() - before; popped != 1 {
		t.Fatalf("popped=%d, want 1", popped)
	}
}
