package syncookie

import (
	"errors"
	"testing"
)

// issueCookie fills the half-open queue with k1 and returns the cookie
// issued for key at the given now.
func issueCookie(t *testing.T, v *Validator, now int64, key Key, cisn uint32, mss int) uint32 {
	t.Helper()
	r, err := v.OnSyn(now, key, cisn, mss)
	if err != nil {
		t.Fatalf("syn for cookie: %v", err)
	}
	if !r.Cookie {
		t.Fatalf("expected cookie for key %+v, got %+v", key, r)
	}
	return r.ISN
}

func newFilled(t *testing.T, b int, timeout int64, a int) *Validator {
	t.Helper()
	v, err := New(b, timeout, a, testHash, isnGen(1000))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v
}

// TestCookieAges: cookie issued at t=2 verifies at age 0 and age 1, and is
// rejected with ErrCookieExpired at age 2.
func TestCookieAges(t *testing.T) {
	// age 0: verify at the same tick (t=2).
	v := newFilled(t, 1, 1_000_000, 4)
	if _, err := v.OnSyn(130000, k1(), 100, 1400); err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	c := issueCookie(t, v, 130001, k2(), 200, 1400)
	if err := v.OnAck(130002, k2(), 201, c+1); err != nil {
		t.Fatalf("age-0 ack: %v", err)
	}
	if s := v.Stats(); s.CookieOK != 1 || s.Pending != 1 {
		t.Fatalf("stats = %+v, want CookieOK=1 Pending=1", s)
	}
	key, mss, err := v.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if key != k2() || mss != 1220 {
		t.Fatalf("accept = (%+v, %d), want (k2, 1220)", key, mss)
	}

	// age 1: issued at t=2 (now=130001), verified at t=3 (now=192000).
	v = newFilled(t, 1, 1_000_000, 4)
	if _, err := v.OnSyn(130000, k1(), 100, 1400); err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	c = issueCookie(t, v, 130001, k2(), 200, 1400)
	if err := v.OnAck(192000, k2(), 201, c+1); err != nil {
		t.Fatalf("age-1 ack: %v", err)
	}

	// age 2: issued at t=2, verified at t=4 (now=256000): expired.
	v = newFilled(t, 1, 1_000_000, 4)
	if _, err := v.OnSyn(130000, k1(), 100, 1400); err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	c = issueCookie(t, v, 130001, k2(), 200, 1400)
	if err := v.OnAck(256000, k2(), 201, c+1); !errors.Is(err, ErrCookieExpired) {
		t.Fatalf("age-2 ack = %v, want ErrCookieExpired", err)
	}
	if s := v.Stats(); s.CookieOK != 0 || s.Pending != 0 {
		t.Fatalf("stats = %+v, want CookieOK=0 Pending=0", s)
	}
}

// TestCookieTickWrap: t5 is 5 bits, so ticks wrap every 32. A cookie issued
// at t=31 verifies at t=32 (age 1) and t=31 (age 0); at t=33 it is expired.
func TestCookieTickWrap(t *testing.T) {
	const tick = 64000
	v := newFilled(t, 1, 1_000_000, 4)
	// Register k1 right before t=31 so it survives until t=33
	// (T=1e6 ms < 31 ticks, so an earlier registration would expire).
	if _, err := v.OnSyn(31*tick, k1(), 100, 1400); err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	// Issue at t=31 for k2 and k4.
	c2 := issueCookie(t, v, 31*tick, k2(), 200, 1400)
	c4 := issueCookie(t, v, 31*tick, k4(), 400, 1400)
	if t5 := c2 >> 27; t5 != 31 {
		t.Fatalf("t5 = %d, want 31", t5)
	}
	// t=32: k2 verifies with age=(32-31)%32=1.
	if err := v.OnAck(32*tick, k2(), 201, c2+1); err != nil {
		t.Fatalf("wrap age-1 ack: %v", err)
	}
	// Issue at t=32 (t5 wraps to 0) and verify at t=32 with age 0.
	c3 := issueCookie(t, v, 32*tick, k3(), 300, 1400)
	if t5 := c3 >> 27; t5 != 0 {
		t.Fatalf("wrapped t5 = %d, want 0", t5)
	}
	if err := v.OnAck(32*tick, k3(), 301, c3+1); err != nil {
		t.Fatalf("wrap age-0 ack: %v", err)
	}
	// t=33: k4's cookie from t=31 has age 2 -> expired.
	if err := v.OnAck(33*tick, k4(), 401, c4+1); !errors.Is(err, ErrCookieExpired) {
		t.Fatalf("wrap age-2 ack = %v, want ErrCookieExpired", err)
	}
	if s := v.Stats(); s.CookieOK != 2 || s.CookieSent != 3 {
		t.Fatalf("stats = %+v, want CookieOK=2 CookieSent=3", s)
	}
}

// TestCookieBadHash: a corrupted hash field yields ErrCookie.
func TestCookieBadHash(t *testing.T) {
	v := newFilled(t, 1, 1_000_000, 4)
	if _, err := v.OnSyn(130000, k1(), 100, 1400); err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	c := issueCookie(t, v, 130001, k2(), 200, 1400)
	bad := c ^ 1 // flip one hash bit, keep t5 and mi intact
	if err := v.OnAck(130002, k2(), 201, bad+1); !errors.Is(err, ErrCookie) {
		t.Fatalf("bad-hash ack = %v, want ErrCookie", err)
	}
	// Wrong seq (cisn mismatch) also fails the hash check.
	if err := v.OnAck(130002, k2(), 999, c+1); !errors.Is(err, ErrCookie) {
		t.Fatalf("bad-seq ack = %v, want ErrCookie", err)
	}
}

// TestCookieBadMI: mi >= 4 yields ErrCookie before any expiry/hash check.
func TestCookieBadMI(t *testing.T) {
	v := newFilled(t, 1, 1_000_000, 4)
	if _, err := v.OnSyn(130000, k1(), 100, 1400); err != nil {
		t.Fatalf("k1 syn: %v", err)
	}
	// Craft a cookie with t5=2, mi=4, arbitrary hash bits.
	c := uint32(2)<<27 | uint32(4)<<24 | 0x123456
	if err := v.OnAck(130002, k2(), 201, c+1); !errors.Is(err, ErrCookie) {
		t.Fatalf("mi=4 ack = %v, want ErrCookie", err)
	}
	c = uint32(2)<<27 | uint32(7)<<24
	if err := v.OnAck(130002, k2(), 201, c+1); !errors.Is(err, ErrCookie) {
		t.Fatalf("mi=7 ack = %v, want ErrCookie", err)
	}
}
