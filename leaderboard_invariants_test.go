package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConstructorValidation(t *testing.T) {
	invalid := []struct {
		L, W, M int64
		K, Hs   int
	}{
		{0, 3, 100, 3, 1},
		{10, 0, 100, 3, 1},
		{10, 3, 0, 3, 1},
		{10, 3, 100, 0, 1},
		{10, 3, 100, 3, 0},
		{1_000_000_001, 3, 100, 3, 1},
		{10, 1001, 100, 3, 1},
		{10, 3, 1_000_000_000_001, 3, 1},
		{10, 3, 100, 101, 1},
		{10, 3, 100, 3, 101},
	}
	for _, tc := range invalid {
		if _, err := NewLeaderboard(tc.L, tc.W, tc.M, tc.K, tc.Hs); err != ErrInvalidArgument {
			t.Fatalf("params=%+v err=%v", tc, err)
		}
	}
}

func TestZeroScoreItemsAreReclaimed(t *testing.T) {
	lb, _ := NewLeaderboard(2, 1, 10, 5, 1)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("ephemeral-%03d", i)
		mustAdd(t, lb, id, 1, int64(i*2))
		if i > 0 {
			mustSnapshot(t, lb, int64(i*2))
		}
	}
	mustSnapshot(t, lb, 200)
	if len(lb.scores) != 0 {
		t.Fatalf("scores retained zero entries: %d", len(lb.scores))
	}
	if len(lb.buckets) > 1 {
		t.Fatalf("buckets retained: %d", len(lb.buckets))
	}
	if len(lb.prev) != 0 {
		t.Fatalf("unqualified items entered prev: %d", len(lb.prev))
	}
}

func TestReplayDeterministic(t *testing.T) {
	run := func() []SnapshotResult {
		lb, _ := NewLeaderboard(7, 4, 30, 4, 2)
		ops := []struct {
			id    string
			d, at int64
		}{
			{"a", 30, 0}, {"b", 40, 3}, {"c", 20, 5},
			{"a", 10, 8}, {"b", 5, 12}, {"c", 35, 18},
			{"d", 30, 20}, {"a", 40, 27},
		}
		for _, op := range ops {
			mustAdd(t, lb, op.id, op.d, op.at)
		}

		var results []SnapshotResult
		for _, now := range []int64{28, 28, 28, 35, 42} {
			results = append(results, mustSnapshot(t, lb, now))
		}
		return results
	}

	first := run()
	second := run()
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("nondeterministic replay:\n%+v\n%+v", first, second)
	}
}

func TestConcurrentOpsAreSerializable(t *testing.T) {
	lb, _ := NewLeaderboard(1, 20, 50, 20, 3)
	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	var nextTime int64
	var timeMu sync.Mutex
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := fmt.Sprintf("g%d-i%d", g, i%20)
				timeMu.Lock()
				at := nextTime
				nextTime += int64(1 + (g+i)%3)
				timeMu.Unlock()
				if err := lb.Add(id, 10, at); err != nil && err != ErrExpired {
					t.Errorf("Add: %v", err)
					return
				}
				if i%5 == 0 {
					result, err := lb.Snapshot(at)
					if err != nil && err != ErrClockRolledBack {
						t.Errorf("Snapshot: %v", err)
						return
					}
					if err != nil {
						continue
					}
					if len(result.Items) > lb.K {
						t.Errorf("items %d > K", len(result.Items))
					}
					for j := 1; j < len(result.Items); j++ {
						if result.Items[j-1].Score < result.Items[j].Score {
							t.Errorf("snapshot not sorted")
							return
						}
						if result.Items[j-1].Score == result.Items[j].Score &&
							result.Items[j-1].ID > result.Items[j].ID {
							t.Errorf("tie not id sorted")
							return
						}
					}
				} else {
					_ = lb.Score(id)
				}
			}
		}(g)
	}
	wg.Wait()
}
