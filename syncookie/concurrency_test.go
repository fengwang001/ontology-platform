package syncookie

import (
	"sync"
	"testing"
)

// TestConcurrentSmoke hammers the validator from many goroutines (run with
// -race) and checks the capacity invariants afterwards.
func TestConcurrentSmoke(t *testing.T) {
	const b, a = 4, 4
	v := newFilled(t, b, 5000, a)
	keys := []Key{k1(), k2(), k3(), k4()}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := int64(g * 1000)
			for i := 0; i < 500; i++ {
				now := base + int64(i)
				key := keys[(g+i)%len(keys)]
				r, err := v.OnSyn(now, key, uint32(i), 1400)
				if err == nil {
					_ = v.OnAck(now, key, uint32(i)+1, r.ISN+1)
				}
				if i%7 == 0 {
					_, _, _ = v.Accept()
				}
				if i%11 == 0 {
					_ = v.Stats()
				}
			}
		}(g)
	}
	wg.Wait()

	s := v.Stats()
	if s.HalfOpen > b || s.Pending > a {
		t.Fatalf("capacity invariant violated: %+v (B=%d A=%d)", s, b, a)
	}
	if s.CookieOK > s.CookieSent {
		t.Fatalf("cookieOK=%d exceeds cookieSent=%d", s.CookieOK, s.CookieSent)
	}
}
