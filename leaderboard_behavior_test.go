package ontology

import "testing"

func TestBucketBoundariesAndLateArrival(t *testing.T) {
	lb, err := NewLeaderboard(10, 3, 1, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, lb, "x", 1, 0)
	assertScore(t, lb, "x", 1)

	mustSnapshot(t, lb, 29)
	assertScore(t, lb, "x", 1)
	mustSnapshot(t, lb, 30)
	if err := lb.Add("boundary", 1, 0); !errorIs(err, ErrExpired) {
		t.Fatalf("bucket 0 at cur=2: %v", err)
	}
	if err := lb.Add("late", 1, 10); err != nil {
		t.Fatalf("bucket cur-W+1: %v", err)
	}

	assertScore(t, lb, "x", 0)
	assertScore(t, lb, "late", 1)

	mustAdd(t, lb, "left", 1, 39)
	mustAdd(t, lb, "right", 1, 40)
	mustSnapshot(t, lb, 40)
	assertScore(t, lb, "late", 0)
	assertScore(t, lb, "left", 1)
	assertScore(t, lb, "right", 1)
	if len(lb.buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(lb.buckets))
	}
}

func TestHysteresisCutoffRoundingAndRecovery(t *testing.T) {
	tests := []struct {
		M      int64
		cutoff int64
	}{
		{1, 1},
		{3, 3},
		{4, 3},
		{100, 75},
	}

	for _, tc := range tests {
		lb, _ := NewLeaderboard(1, 2, tc.M, 10, 2)
		mustAddScore(t, lb, "x", tc.M, 0)
		first := mustSnapshot(t, lb, 0)
		if len(first.Items) != 1 || first.Items[0].Score != tc.M {
			t.Fatalf("M=%d initial = %+v", tc.M, first.Items)
		}

		mustAddScore(t, lb, "x", tc.cutoff, 2)
		second := mustSnapshot(t, lb, 2)
		if tc.cutoff == tc.M {
			if len(second.Items) != 1 || lb.prev["x"].hc != 0 {
				t.Fatalf("M=%d cutoff==M result=%+v hc=%d", tc.M, second.Items, lb.prev["x"].hc)
			}
			dropped := mustSnapshot(t, lb, 4)
			if len(dropped.Items) != 0 {
				t.Fatalf("M=%d expected expiry drop, got %+v", tc.M, dropped.Items)
			}
			continue
		}
		if len(second.Items) != 1 || second.Items[0].Score != tc.cutoff || lb.prev["x"].hc != 1 {
			t.Fatalf("M=%d hysteresis result=%+v hc=%d", tc.M, second.Items, lb.prev["x"].hc)
		}

		third := mustSnapshot(t, lb, 2)
		if len(third.Items) != 1 || third.Items[0].Score != tc.cutoff {
			t.Fatalf("M=%d hc==Hs-1 should stay, got %+v", tc.M, third.Items)
		}
		if hc := lb.prev["x"].hc; hc != 2 {
			t.Fatalf("M=%d hc after second low snapshot=%d", tc.M, hc)
		}

		fourth := mustSnapshot(t, lb, 3)
		if len(fourth.Items) != 0 {
			t.Fatalf("M=%d hc==Hs should drop, got %+v", tc.M, fourth.Items)
		}

		mustAddScore(t, lb, "x", tc.cutoff, 4)
		fifth := mustSnapshot(t, lb, 4)
		if len(fifth.Items) != 0 {
			t.Fatalf("M=%d dropped item must reach M, got %+v", tc.M, fifth.Items)
		}

		if tc.M > tc.cutoff {
			mustAdd(t, lb, "x", tc.M-tc.cutoff, 4)
		}
		recovered := mustSnapshot(t, lb, 4)
		if len(recovered.Items) != 1 || recovered.Items[0].Score != tc.M {
			t.Fatalf("M=%d recovery = %+v", tc.M, recovered.Items)
		}
		if hc := lb.prev["x"].hc; hc != 0 {
			t.Fatalf("M=%d recovered hc=%d, want 0", tc.M, hc)
		}
	}
}

func TestTiedRanksAndKTruncation(t *testing.T) {
	lb, _ := NewLeaderboard(1, 10, 1, 10, 1)
	scores := map[string]int64{"a": 4, "b": 3, "c": 3, "d": 2}
	for id, score := range scores {
		mustAddScore(t, lb, id, score, 0)
	}
	result := mustSnapshot(t, lb, 0)
	want := []RankedItem{
		{ID: "a", Score: 4, Rank: 1, New: true},
		{ID: "b", Score: 3, Rank: 2, New: true},
		{ID: "c", Score: 3, Rank: 2, New: true},
		{ID: "d", Score: 2, Rank: 4, New: true},
	}
	assertResult(t, result, want, nil)

	lb, _ = NewLeaderboard(1, 10, 1, 3, 1)
	for id, score := range scores {
		mustAddScore(t, lb, id, score, 0)
	}
	mustSnapshot(t, lb, 0)
	mustAddScore(t, lb, "d", 2, 1)
	result = mustSnapshot(t, lb, 1)
	assertResult(t, result,
		[]RankedItem{
			{ID: "a", Score: 4, Rank: 1, Change: 0},
			{ID: "d", Score: 4, Rank: 1, New: true},
			{ID: "b", Score: 3, Rank: 3, Change: -1},
		},
		[]DroppedItem{{ID: "c", PrevRank: 2}})
}

func TestPeekDoesNotAdvanceOrCountHysteresis(t *testing.T) {
	lb, _ := NewLeaderboard(1, 10, 100, 5, 2)
	mustAddScore(t, lb, "x", 100, 0)
	mustSnapshot(t, lb, 0)

	peeked := mustPeek(t, lb, 20)
	if len(peeked.Items) != 0 {
		t.Fatalf("expired peek = %+v", peeked.Items)
	}
	if !lb.hasClock || lb.clock != 0 || len(lb.prev) != 1 {
		t.Fatalf("peek changed state: clock=%d prev=%+v", lb.clock, lb.prev)
	}

	for i := 0; i < 2; i++ {
		result := mustPeek(t, lb, 0)
		if len(result.Items) != 1 || result.Items[0].Score != 100 {
			t.Fatalf("stable peek %d = %+v", i, result.Items)
		}
	}
	if hc := lb.prev["x"].hc; hc != 0 {
		t.Fatalf("peek incremented hc=%d", hc)
	}

	first := mustSnapshot(t, lb, 0)
	if len(first.Items) != 1 || first.Items[0].Change != 0 {
		t.Fatalf("repeat snapshot = %+v", first.Items)
	}
}

func TestScoreOverflow(t *testing.T) {
	lb, _ := NewLeaderboard(100, 10, 1, 10, 1)
	mustAdd(t, lb, "x", 1_000_000_000, 0)
	for i := 0; i < 999_999; i++ {
		mustAdd(t, lb, "x", 1_000_000_000, 0)
	}
	if err := lb.Add("x", 1, 1); !errorIs(err, ErrScoreOverflow) {
		t.Fatalf("add overflow: %v", err)
	}
	assertScore(t, lb, "x", 1_000_000_000_000_000)
	if lb.clock != 0 {
		t.Fatalf("overflow advanced clock to %d", lb.clock)
	}
}

func TestAddDoesNotScanWindowBuckets(t *testing.T) {
	for _, W := range []int64{10, 1000} {
		lb, _ := NewLeaderboard(1, W, 1, 100, 1)
		for bucket := int64(0); bucket < W; bucket++ {
			mustAdd(t, lb, "fill", 1, bucket)
		}
		lb.addBucketLookups = 0
		lb.windowBucketRangeScan = 0
		before := lb.expiredBucketPops

		mustAdd(t, lb, "late", 1, W)

		if lb.addBucketLookups != 1 {
			t.Fatalf("W=%d Add bucket lookups=%d", W, lb.addBucketLookups)
		}
		if lb.windowBucketRangeScan != 0 || lb.expiredBucketPops-before != 1 {
			t.Fatalf("W=%d Add touched unexpected buckets: scans=%d pops=%d", W, lb.windowBucketRangeScan, lb.expiredBucketPops-before)
		}
	}
}

func mustAdd(t *testing.T, lb *Leaderboard, id string, d, at int64) {
	t.Helper()
	if err := lb.Add(id, d, at); err != nil {
		t.Fatal(err)
	}
}

func mustAddScore(t *testing.T, lb *Leaderboard, id string, total, at int64) {
	t.Helper()
	for total > 0 {
		d := int64(1_000_000_000)
		if total < d {
			d = total
		}
		if err := lb.Add(id, d, at); err != nil {
			t.Fatal(err)
		}
		total -= d
	}
}

func mustSnapshot(t *testing.T, lb *Leaderboard, now int64) SnapshotResult {
	t.Helper()
	result, err := lb.Snapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustPeek(t *testing.T, lb *Leaderboard, now int64) SnapshotResult {
	t.Helper()
	result, err := lb.Peek(now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertScore(t *testing.T, lb *Leaderboard, id string, want int64) {
	t.Helper()
	if got := lb.Score(id); got != want {
		t.Fatalf("Score(%q) = %d, want %d", id, got, want)
	}
}

func errorIs(got, want error) bool {
	return got == want
}
