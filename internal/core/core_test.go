package core

import (
	"fmt"
	"testing"
)

// Settle must not touch share records that were fully paid off in the
// past: with 100 vs 10000 historical paid-off shares, the touched count
// of an identical Settle must be the same.
func TestSettleTouchedIndependentOfPaidHistory(t *testing.T) {
	for _, k := range []int{100, 10000} {
		t.Run(fmt.Sprintf("paid=%d", k), func(t *testing.T) {
			e, err := New(0, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.SetSplit(0, "c", []Part{{Creator: "alice", Bps: 10000}}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < k; i++ {
				if _, err := e.Earn(int64(i), fmt.Sprintf("e%d", i), "c", 10); err != nil {
					t.Fatal(err)
				}
			}
			now := int64(k)
			pay, _, err := e.Settle(now, "alice")
			if err != nil || pay != int64(10*k) {
				t.Fatalf("first settle: pay=%d err=%v", pay, err)
			}
			if _, err := e.Earn(now, "extra", "c", 7); err != nil {
				t.Fatal(err)
			}
			pay, _, err = e.Settle(now, "alice")
			if err != nil || pay != 7 {
				t.Fatalf("second settle: pay=%d err=%v", pay, err)
			}
			// Only the single live share may be examined, regardless of k.
			if e.touched != 1 {
				t.Fatalf("touched=%d, want 1 (independent of %d paid-off shares)", e.touched, k)
			}
		})
	}
}

// touched must stay within consumed + held + immature + 2 per Settle.
func TestSettleTouchedBound(t *testing.T) {
	e, err := New(100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetSplit(0, "c", []Part{{Creator: "alice", Bps: 10000}}); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err1 := e.Earn(0, "e1", "c", 10)
	must(err1) // matures at 100
	_, err2 := e.Earn(10, "e2", "c", 10)
	must(err2) // matures at 110
	must(e.Hold(20, "h1", "c", 0, 5))
	_, err3 := e.Earn(30, "e3", "c", 10)
	must(err3) // matures at 130

	// At now=110: e1 held+mature, e2 mature (consumed), e3 immature.
	pay, _, err := e.Settle(110, "alice")
	if err != nil || pay != 10 {
		t.Fatalf("pay=%d err=%v", pay, err)
	}
	consumed, heldN, immatureN := 1, 1, 1
	if e.touched != 2 {
		t.Fatalf("touched=%d, want exactly 2 (held e1, consumed e2; loop stops before e3)", e.touched)
	}
	if bound := consumed + heldN + immatureN + 2; e.touched > bound {
		t.Fatalf("touched=%d exceeds bound %d", e.touched, bound)
	}

	// Net below Min with a large available remainder: the untouched
	// available shares must not be scanned (touched stays minimal).
	e2, err := New(0, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	must2 := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must2(e2.SetSplit(0, "c", []Part{{Creator: "bob", Bps: 10000}}))
	for i := 0; i < 500; i++ {
		_, err := e2.Earn(int64(i), fmt.Sprintf("g%d", i), "c", 10)
		must2(err)
	}
	// A = 5000 < Min, debt = 0: nothing consumed, payout 0.
	pay, _, err = e2.Settle(500, "bob")
	if err != nil || pay != 0 {
		t.Fatalf("pay=%d err=%v", pay, err)
	}
	if e2.touched > 2 {
		t.Fatalf("touched=%d exceeds bound 2 when nothing is consumed", e2.touched)
	}
}
