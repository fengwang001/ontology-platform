package quota

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestReserveBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		quota int64
		used  int64 // committed before the check
		d     int64
		want  bool
	}{
		{"default quota zero blocks growth", 0, 0, 1, false},
		{"zero delta always passes", 0, 0, 0, true},
		{"negative delta always passes", 0, 0, -5, true},
		{"U+d exactly Q passes", 100, 60, 40, true},
		{"U+d one over Q fails", 100, 60, 41, false},
		{"over-quota: d=0 passes", 80, 90, 0, true},
		{"over-quota: d=1 fails", 80, 90, 1, false},
		{"over-quota: shrink passes", 80, 90, -80, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := New()
			l.SetQuota("t", max(tc.quota, tc.used))
			if tc.used > 0 {
				tok, ok := l.Reserve("t", tc.used)
				if !ok {
					t.Fatalf("setup reserve of %d failed", tc.used)
				}
				tok.Commit()
			}
			l.SetQuota("t", tc.quota) // always accepted, may enter over-quota state
			tok, ok := l.Reserve("t", tc.d)
			if ok != tc.want {
				t.Fatalf("Reserve(d=%d) ok = %v, want %v", tc.d, ok, tc.want)
			}
			if ok {
				tok.Commit()
				if got := l.Usage("t"); got != tc.used+tc.d {
					t.Errorf("Usage = %d, want %d", got, tc.used+tc.d)
				}
			}
		})
	}
}

func TestPendingReservationVisible(t *testing.T) {
	l := New()
	l.SetQuota("t", 100)
	tok, ok := l.Reserve("t", 60)
	if !ok {
		t.Fatal("first reserve failed")
	}
	// A concurrent writer sees U + pending reservations: 0+60+41 > 100.
	if _, ok := l.Reserve("t", 41); ok {
		t.Error("reserve of 41 should fail while 60 is pending")
	}
	if _, ok := l.Reserve("t", 40); !ok {
		t.Error("reserve of 40 should pass: 0+60+40 == 100")
	}
	// Release rolls back: the reserved headroom returns.
	tok.Release()
	if got := l.Reserved("t"); got != 40 {
		t.Errorf("Reserved = %d, want 40", got)
	}
	if got := l.Usage("t"); got != 0 {
		t.Errorf("Usage = %d, want 0 after release", got)
	}
}

func TestOnlyPositiveDeltaReserved(t *testing.T) {
	l := New()
	l.SetQuota("t", 100)
	tok, ok := l.Reserve("t", -30)
	if !ok {
		t.Fatal("negative reserve failed")
	}
	if got := l.Reserved("t"); got != 0 {
		t.Errorf("Reserved = %d, want 0 for negative delta", got)
	}
	tok.Commit()
	if got := l.Usage("t"); got != -30 {
		t.Errorf("Usage = %d, want -30", got)
	}
}

func TestConcurrentReservesNeverExceedQuota(t *testing.T) {
	l := New()
	l.SetQuota("t", 1000)
	var granted atomic.Int64
	var wg sync.WaitGroup
	tokens := make(chan *Token, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, ok := l.Reserve("t", 10); ok {
				granted.Add(1)
				tokens <- tok
			}
		}()
	}
	wg.Wait()
	close(tokens)
	if got := granted.Load(); got != 100 {
		t.Fatalf("granted = %d, want exactly 100", got)
	}
	for tok := range tokens {
		tok.Commit()
	}
	if got := l.Usage("t"); got != 1000 {
		t.Errorf("Usage = %d, want 1000", got)
	}
	if _, ok := l.Reserve("t", 1); ok {
		t.Error("reserve past full quota should fail")
	}
}

func TestTokenSingleUse(t *testing.T) {
	l := New()
	l.SetQuota("t", 100)
	tok, _ := l.Reserve("t", 10)
	tok.Commit()
	defer func() {
		if recover() == nil {
			t.Error("second Commit should panic")
		}
	}()
	tok.Commit()
}
