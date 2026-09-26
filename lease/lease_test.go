package lease_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/lease"
)

// TestExpiredMatchesNaive pins invariant 1: after any random sequence of
// grants/renews, Expired(now) must equal the naive now >= expiry compare
// against a model expiry tracked by the test.
func TestExpiredMatchesNaive(t *testing.T) {
	const TTL = 7
	cases := []struct {
		seed int64
	}{{1}, {2}, {42}, {99}}
	for _, tc := range cases {
		t.Run("naive", func(t *testing.T) {
			rng := rand.New(rand.NewSource(tc.seed))
			l := lease.New(TTL)
			modelExp, modelTok, now := 0, 0, 0
			for step := 0; step < 200; step++ {
				now += rng.Intn(6)
				switch rng.Intn(2) {
				case 0:
					modelTok++
					modelExp = now + TTL
					if got := l.Grant("o", now); got != modelTok {
						t.Fatalf("step %d: token %d want %d", step, got, modelTok)
					}
				case 1:
					err := l.Renew(modelTok, now)
					switch {
					case now >= modelExp:
						if !errors.Is(err, lease.ErrExpired) {
							t.Fatalf("step %d: want ErrExpired, got %v", step, err)
						}
					default:
						if err != nil {
							t.Fatalf("step %d: unexpected %v", step, err)
						}
						modelExp = now + TTL
					}
				}
				for _, probe := range []int{now - 1, now, modelExp - 1, modelExp, modelExp + 1} {
					if probe < 0 {
						continue
					}
					want := probe >= modelExp
					if got := l.Expired(probe); got != want {
						t.Fatalf("step %d: Expired(%d)=%v want %v (expiry %d)", step, probe, got, want, modelExp)
					}
				}
			}
		})
	}
}

// TestFencingMonotonic pins invariant 2: every grant strictly raises the
// token and only the current token can renew.
func TestFencingMonotonic(t *testing.T) {
	l := lease.New(10)
	prev := 0
	for i := 1; i <= 5; i++ {
		tok := l.Grant("o", i)
		if tok != prev+1 {
			t.Fatalf("grant %d: token %d, want %d (must be strictly +1)", i, tok, prev+1)
		}
		if tok <= prev {
			t.Fatalf("token not strictly increasing: %d -> %d", prev, tok)
		}
		prev = tok
	}
	staleCases := []struct {
		token, now int
	}{
		{0, 6},                               // the pre-grant token
		{1, 6}, {prev - 1, 6}, {prev + 1, 6}, // old and future tokens
	}
	for _, c := range staleCases {
		err := l.Renew(c.token, c.now)
		if !errors.Is(err, lease.ErrStaleToken) {
			t.Fatalf("Renew(%d,%d): want ErrStaleToken, got %v", c.token, c.now, err)
		}
	}
	if l.Token() != prev || l.Expiry() != 15 {
		t.Fatalf("fenced renews moved state: token=%d expiry=%d", l.Token(), l.Expiry())
	}
}

// TestRenewOnlyWhileAlive pins invariant 3: renewal succeeds only with
// now < expiry; the boundary itself is refused.
func TestRenewOnlyWhileAlive(t *testing.T) {
	cases := []struct {
		name       string
		ttl, grant int
		renewAt    int
		wantErr    error
		wantExpiry int
	}{
		{"just before expiry", 10, 0, 9, nil, 19},
		{"one after grant", 10, 0, 1, nil, 11},
		{"at expiry is refused", 10, 0, 10, lease.ErrExpired, 10},
		{"after expiry is refused", 10, 0, 11, lease.ErrExpired, 10},
		{"small ttl boundary", 3, 5, 7, nil, 10},
		{"small ttl at expiry", 3, 5, 8, lease.ErrExpired, 8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := lease.New(c.ttl)
			tok := l.Grant("o", c.grant)
			err := l.Renew(tok, c.renewAt)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v want %v", err, c.wantErr)
			}
			if l.Expiry() != c.wantExpiry {
				t.Fatalf("expiry=%d want %d", l.Expiry(), c.wantExpiry)
			}
		})
	}
}
