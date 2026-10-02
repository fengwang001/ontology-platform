package cinema

import (
	"errors"
	"testing"
)

func mustRegistry(t *testing.T, rows, width int, ttl int64) *Registry {
	t.Helper()
	r, err := NewRegistry(rows, width, ttl)
	if err != nil {
		t.Fatalf("NewRegistry(%d,%d,%d): %v", rows, width, ttl, err)
	}
	return r
}

func mustHold(t *testing.T, r *Registry, k int, now int64) (id, row, start int) {
	t.Helper()
	id, row, start, err := r.Hold(k, now)
	if err != nil {
		t.Fatalf("Hold(k=%d, now=%d): %v", k, now, err)
	}
	t.Logf("Hold(k=%d, now=%d) -> id=%d row=%d s=%d", k, now, id, row, start)
	return id, row, start
}

func mustConfirm(t *testing.T, r *Registry, id int, now int64) {
	t.Helper()
	if err := r.Confirm(id, now); err != nil {
		t.Fatalf("Confirm(id=%d, now=%d): %v", id, now, err)
	}
	t.Logf("Confirm(id=%d, now=%d) -> ok", id, now)
}

func mustRelease(t *testing.T, r *Registry, id int, now int64) {
	t.Helper()
	if err := r.Release(id, now); err != nil {
		t.Fatalf("Release(id=%d, now=%d): %v", id, now, err)
	}
	t.Logf("Release(id=%d, now=%d) -> ok", id, now)
}

func expectErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got err=%v, want %v", what, err, want)
	}
	t.Logf("%s -> rejected as expected: %v", what, err)
}

func TestNewRegistryValidation(t *testing.T) {
	cases := []struct {
		rows, width int
		ttl         int64
	}{
		{0, 10, 5}, {27, 10, 5}, {-1, 10, 5},
		{10, 0, 5}, {10, 41, 5}, {10, -3, 5},
		{10, 10, 0}, {10, 10, -2},
	}
	for _, c := range cases {
		if _, err := NewRegistry(c.rows, c.width, c.ttl); !errors.Is(err, ErrInvalidDimensions) {
			t.Errorf("NewRegistry(%d,%d,%d): got %v, want ErrInvalidDimensions",
				c.rows, c.width, c.ttl, err)
		}
	}
	for _, c := range []struct {
		rows, width int
		ttl         int64
	}{{1, 1, 1}, {26, 40, 1}, {26, 40, 100}} {
		if _, err := NewRegistry(c.rows, c.width, c.ttl); err != nil {
			t.Errorf("NewRegistry(%d,%d,%d): unexpected %v", c.rows, c.width, c.ttl, err)
		}
	}
}

// checkCounts verifies free+held+confirmed == rows*width at now.
func checkCounts(t *testing.T, r *Registry, now int64, rows, width int) {
	t.Helper()
	var n [3]int
	for _, row := range r.Seats(now) {
		for _, st := range row {
			n[st]++
		}
	}
	if n[0]+n[1]+n[2] != rows*width {
		t.Fatalf("seat counts %v do not sum to %d", n, rows*width)
	}
}

// Orphan detection on both sides plus the wall-adjacent lone free seat.
// Layout after confirming seat 3 of 6: free = {1,2,4,5,6}.
//
//	s=1: right run {2} len 1            -> orphan
//	s=2: left run {1} len 1 (wall side) -> orphan
//	s=4: left run 0, right run {5,6} 2  -> clean, wins pass 1
//	s=5: left run {4} len 1             -> orphan
//	s=6: left run {5} len 1             -> orphan
func TestOrphanBothSidesAndWall(t *testing.T) {
	r := mustRegistry(t, 1, 6, 100)
	id, row, s := mustHold(t, r, 1, 0)
	if id != 1 || row != 1 || s != 3 {
		t.Fatalf("first hold = id %d row %d s %d, want id 1 row 1 s 3", id, row, s)
	}
	mustConfirm(t, r, id, 0)
	id, row, s = mustHold(t, r, 1, 1)
	t.Logf("判定依据: s=4 左侧紧邻占用(长度0)、右侧空闲区{5,6}长度2，是唯一无孤座候选")
	if id != 2 || row != 1 || s != 4 {
		t.Fatalf("second hold = id %d row %d s %d, want id 2 row 1 s 4", id, row, s)
	}
	checkCounts(t, r, 1, 1, 6)
}

// A clean candidate in a later row beats orphan-only candidates in
// earlier rows. Setup occupies row 1 seats {1,2,3,4} leaving free =
// {5,6} (both k=1 candidates orphan); row 2 is empty and has clean
// candidates, so the next k=1 hold lands in row 2.
func TestCleanBackRowBeatsOrphanFrontRow(t *testing.T) {
	r := mustRegistry(t, 2, 6, 100)
	// k=1 fills row 1 seats 3 then 4; a k=2 hold then takes [1,2]
	// (clean: both neighbours occupied/wall, dev tie with [5,6] -> s=1).
	steps := []struct{ k, wantRow, wantS int }{
		{1, 1, 3}, {1, 1, 4}, {2, 1, 1},
	}
	for i, st := range steps {
		id, row, s := mustHold(t, r, st.k, int64(i))
		if row != st.wantRow || s != st.wantS {
			t.Fatalf("hold id=%d = row %d s %d, want row %d s %d", id, row, s, st.wantRow, st.wantS)
		}
		mustConfirm(t, r, id, int64(i))
	}
	// Row 1 free = {5,6}: s=5 right run {6} orphan, s=6 left run {5} orphan.
	// Pass 1 is non-empty thanks to row 2, so the hold must skip row 1.
	id, row, s := mustHold(t, r, 1, 3)
	t.Logf("判定依据: 排1 仅剩孤座候选, 排2 有无孤座候选, 第一遍即选中排2")
	if row != 2 || s != 3 {
		t.Fatalf("hold %d = row %d s %d, want row 2 s 3", id, row, s)
	}
	checkCounts(t, r, 3, 2, 6)
}

// Pass 2 fallback: every candidate is an orphan, and the center
// deviation tie (even W) is broken by the smaller start.
// Layout after confirming seats 3,4 of 6: free = {1,2,5,6}.
// All four candidates are orphan; dev: s=2 -> 3, s=5 -> 3 (tie), so s=2.
func TestFallbackPass2AndDevTie(t *testing.T) {
	r := mustRegistry(t, 1, 6, 100)
	id, _, s := mustHold(t, r, 1, 0)
	if s != 3 {
		t.Fatalf("first hold s=%d, want 3", s)
	}
	mustConfirm(t, r, id, 0)
	id, _, s = mustHold(t, r, 1, 1)
	if s != 4 {
		t.Fatalf("second hold s=%d, want 4", s)
	}
	mustConfirm(t, r, id, 1)
	_, _, s = mustHold(t, r, 1, 2)
	t.Logf("判定依据: 全部候选均有孤座, 退到第二遍; dev(2)=dev(5)=3 并列取小 s")
	if s != 2 {
		t.Fatalf("fallback hold s=%d, want 2", s)
	}
}

// k equal to the full row width.
func TestFullRowParty(t *testing.T) {
	r := mustRegistry(t, 2, 8, 100)
	id, row, s := mustHold(t, r, 8, 0)
	if id != 1 || row != 1 || s != 1 {
		t.Fatalf("hold = id %d row %d s %d, want id 1 row 1 s 1", id, row, s)
	}
	_, row, s = mustHold(t, r, 8, 1)
	if row != 2 || s != 1 {
		t.Fatalf("hold = row %d s %d, want row 2 s 1", row, s)
	}
	_, _, _, err := r.Hold(8, 2)
	expectErr(t, "Hold(k=8) on full house", err, ErrNoSeats)
}

// A hold expires exactly at expiry: at now == expiry its seats are
// selectable again while Confirm reports expired.
func TestExpiryBoundary(t *testing.T) {
	r := mustRegistry(t, 1, 5, 5)
	id, _, s := mustHold(t, r, 1, 10) // expiry = 15
	if s != 3 {
		t.Fatalf("hold s=%d, want 3", s)
	}
	if got := r.Seats(14)[0][2]; got != Held {
		t.Fatalf("seat 3 at now=14 = %v, want held", got)
	}
	if got := r.Seats(15)[0][2]; got != Free {
		t.Fatalf("seat 3 at now=15 = %v, want free (expired exactly at expiry)", got)
	}
	expectErr(t, "Confirm at now==expiry", r.Confirm(id, 15), ErrHoldExpired)
	id2, _, s2 := mustHold(t, r, 1, 15)
	t.Logf("判定依据: id=%d expiry=15, now=15 已过期, 座位立即可被 id=%d 再选中", id, id2)
	if s2 != 3 {
		t.Fatalf("re-hold s=%d, want 3 (expired seat reusable)", s2)
	}

	// Just before expiry the same Confirm succeeds.
	r2 := mustRegistry(t, 1, 5, 5)
	id, _, _ = mustHold(t, r2, 1, 10)
	mustConfirm(t, r2, id, 14)
	if got := r2.Seats(14)[0][2]; got != Confirmed {
		t.Fatalf("seat 3 at now=14 = %v, want confirmed", got)
	}
}

// Released seats are immediately selectable again.
func TestReleaseFreesSeatsImmediately(t *testing.T) {
	r := mustRegistry(t, 1, 5, 100)
	id1, _, s1 := mustHold(t, r, 1, 0) // s=3
	if s1 != 3 {
		t.Fatalf("first hold s=%d, want 3", s1)
	}
	mustHold(t, r, 1, 0) // id2, s=2 (all-orphan fallback, dev tie)
	mustRelease(t, r, id1, 1)
	if got := r.Seats(1)[0][2]; got != Free {
		t.Fatalf("seat 3 after release = %v, want free", got)
	}
	_, _, s3 := mustHold(t, r, 1, 1)
	t.Logf("判定依据: id=%d 已释放, 座位 3 立即空闲且为最优无孤座候选", id1)
	if s3 != 3 {
		t.Fatalf("hold after release s=%d, want 3 (released seat)", s3)
	}
}

// Confirm/Release rejection reasons and their priority order.
func TestConfirmReleaseRejections(t *testing.T) {
	r := mustRegistry(t, 1, 5, 5)

	expectErr(t, "Confirm(never issued)", r.Confirm(999, 0), ErrHoldNotFound)
	expectErr(t, "Release(id=0)", r.Release(0, 0), ErrHoldNotFound)

	id1, _, _ := mustHold(t, r, 1, 0) // expiry 5
	mustConfirm(t, r, id1, 1)
	expectErr(t, "Release(confirmed)", r.Release(id1, 2), ErrHoldConfirmed)
	expectErr(t, "Confirm(confirmed)", r.Confirm(id1, 2), ErrHoldConfirmed)
	// Confirmed beats expired.
	expectErr(t, "Confirm(confirmed, past expiry)", r.Confirm(id1, 100), ErrHoldConfirmed)
	expectErr(t, "Release(confirmed, past expiry)", r.Release(id1, 100), ErrHoldConfirmed)

	id2, _, _ := mustHold(t, r, 1, 1) // expiry 6
	mustRelease(t, r, id2, 2)
	expectErr(t, "Release(released)", r.Release(id2, 3), ErrHoldReleased)
	expectErr(t, "Confirm(released)", r.Confirm(id2, 3), ErrHoldReleased)
	// Released beats expired.
	expectErr(t, "Confirm(released, past expiry)", r.Confirm(id2, 200), ErrHoldReleased)

	id3, _, _ := mustHold(t, r, 1, 3) // expiry 8
	expectErr(t, "Confirm(expired)", r.Confirm(id3, 8), ErrHoldExpired)
	expectErr(t, "Release(expired)", r.Release(id3, 9), ErrHoldExpired)
}

// Rejected operations must not change seats, holds or the max seen now.
func TestRejectedOpsKeepState(t *testing.T) {
	r := mustRegistry(t, 1, 5, 5)
	id1, _, _ := mustHold(t, r, 1, 5) // maxNow=5, expiry=10

	snapshot := r.Seats(5)

	_, _, _, err := r.Hold(0, 7)
	expectErr(t, "Hold(k=0)", err, ErrInvalidPartySize)
	_, _, _, err = r.Hold(9, 7)
	expectErr(t, "Hold(k=9)", err, ErrInvalidPartySize)
	// maxNow must still be 5: now=6 would regress if 7 had been accepted.
	id2, _, _ := mustHold(t, r, 1, 6)

	_, _, _, err = r.Hold(1, 4)
	expectErr(t, "Hold(clock regression)", err, ErrClockRegression)
	expectErr(t, "Confirm(clock regression)", r.Confirm(id2, 5), ErrClockRegression)
	expectErr(t, "Release(clock regression)", r.Release(id1, 5), ErrClockRegression)

	// Failed Confirm (expired) must not raise maxNow either.
	expectErr(t, "Confirm(expired)", r.Confirm(id1, 100), ErrHoldExpired)
	mustHold(t, r, 1, 7) // ok because maxNow is still 6, not 100

	// Seats view is unchanged by every rejected op above.
	after := r.Seats(5)
	for i := range snapshot {
		for j := range snapshot[i] {
			if snapshot[i][j] != after[i][j] {
				t.Fatalf("Seats(5) changed by rejected ops at [%d][%d]", i, j)
			}
		}
	}

	// Seats neither checks nor updates maxNow.
	_ = r.Seats(-1000)
	mustHold(t, r, 1, 7)
}

// Hold ids are issued from 1, increment by 1 and are never consumed by
// rejected operations.
func TestHoldIDsGapless(t *testing.T) {
	r := mustRegistry(t, 1, 40, 100)
	for want := 1; want <= 5; want++ {
		id, _, _ := mustHold(t, r, 1, int64(want))
		if id != want {
			t.Fatalf("hold id=%d, want %d", id, want)
		}
		// Rejected holds in between must not consume ids.
		_, _, _, _ = r.Hold(0, int64(want))
		_, _, _, _ = r.Hold(99, int64(want))
	}
}
