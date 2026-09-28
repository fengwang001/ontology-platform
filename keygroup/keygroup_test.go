package keygroup

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestNewValidation covers every distinguishable constructor rejection.
func TestNewValidation(t *testing.T) {
	cases := []struct {
		name        string
		groupCount  int
		parallelism int
		want        error
	}{
		{"zero groups", 0, 1, ErrInvalidGroupCount},
		{"negative groups", -3, 1, ErrInvalidGroupCount},
		{"zero parallelism", 10, 0, ErrInvalidParallelism},
		{"negative parallelism", 10, -1, ErrInvalidParallelism},
		{"parallelism above groups", 10, 11, ErrInvalidParallelism},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.groupCount, tc.parallelism, WithLogger(discardLogger()))
			if !errors.Is(err, tc.want) {
				t.Fatalf("New(%d,%d) error = %v, want %v", tc.groupCount, tc.parallelism, err, tc.want)
			}
		})
	}

	r, err := New(8, 2, WithLogger(nil))
	if !errors.Is(err, ErrInvalidLogger) || r != nil {
		t.Fatalf("WithLogger(nil): router=%v err=%v, want nil, %v", r, err, ErrInvalidLogger)
	}
}

// TestRangePartition verifies, across many shapes, that instance ranges are
// pairwise disjoint contiguous intervals covering every key group exactly
// once, and that the per-group ownership table agrees with the ranges.
func TestRangePartition(t *testing.T) {
	shapes := [][2]int{
		{1, 1}, {2, 1}, {2, 2}, {7, 1}, {7, 3}, {7, 7},
		{10, 3}, {10, 4}, {12, 5}, {37, 6}, {100, 13},
	}
	for _, shape := range shapes {
		groupCount, parallelism := shape[0], shape[1]
		t.Run(fmt.Sprintf("groups=%d,p=%d", groupCount, parallelism), func(t *testing.T) {
			got := ranges(groupCount, parallelism)
			if len(got) != parallelism {
				t.Fatalf("got %d ranges, want %d", len(got), parallelism)
			}

			covered := make([]int, groupCount)
			prevEnd := -1
			for index, rng := range got {
				if rng.Instance != index {
					t.Fatalf("range[%d] has Instance=%d", index, rng.Instance)
				}
				if rng.Start != prevEnd+1 || rng.End < rng.Start {
					t.Fatalf("non-contiguous range %+v after end %d", rng, prevEnd)
				}
				for group := rng.Start; group <= rng.End; group++ {
					covered[group]++
				}
				prevEnd = rng.End
			}
			if prevEnd != groupCount-1 {
				t.Fatalf("ranges end at %d, want %d", prevEnd, groupCount-1)
			}
			for group, count := range covered {
				if count != 1 {
					t.Fatalf("group %d covered %d times, want exactly once", group, count)
				}
			}

			owners := ownership(groupCount, parallelism)
			if len(owners) != groupCount {
				t.Fatalf("ownership length = %d, want %d", len(owners), groupCount)
			}
			for group, instance := range owners {
				if want := ownerOf(group, groupCount, parallelism); instance != want {
					t.Fatalf("group %d owner = %d, want %d", group, instance, want)
				}
				rng := got[instance]
				if group < rng.Start || group > rng.End {
					t.Fatalf("group %d assigned to instance %d but outside range %+v", group, instance, rng)
				}
			}
		})
	}
}

func makeKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("key-%03d", i)
	}
	return keys
}

func seedRouter(t *testing.T, groupCount, parallelism, keyCount int) (*Router, map[string][]byte) {
	t.Helper()
	r, err := New(groupCount, parallelism, WithLogger(discardLogger()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stored := make(map[string][]byte, keyCount)
	for _, key := range makeKeys(keyCount) {
		value := []byte("value-of-" + key)
		if err := r.Put(key, value); err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
		stored[key] = value
	}
	return r, stored
}

// verifyConservation checks that a rescale Result keeps every key exactly
// once, migrates precisely the groups whose owner changed, and leaves values
// byte-identical.
func verifyConservation(t *testing.T, r *Router, oldP, newP int, result *Result, stored map[string][]byte) {
	t.Helper()
	if result.NewParallelism != newP || result.OldParallelism != oldP {
		t.Fatalf("parallelism fields = %d->%d, want %d->%d", result.OldParallelism, result.NewParallelism, oldP, newP)
	}
	if !reflect.DeepEqual(result.Ownership, ownership(r.groupCount, newP)) {
		t.Fatalf("ownership table mismatch for parallelism %d", newP)
	}
	if !reflect.DeepEqual(result.Ranges, ranges(r.groupCount, newP)) {
		t.Fatalf("ranges mismatch for parallelism %d", newP)
	}

	oldOwners := ownership(r.groupCount, oldP)
	newOwners := ownership(r.groupCount, newP)

	wantMovedGroups := map[int]bool{}
	wantMovedKeys := 0
	for key := range stored {
		group := GroupOf(key, r.groupCount)
		if oldOwners[group] != newOwners[group] {
			wantMovedKeys++
			wantMovedGroups[group] = true
		}
	}
	if result.MovedKeyNum != wantMovedKeys {
		t.Fatalf("MovedKeyNum = %d, want %d", result.MovedKeyNum, wantMovedKeys)
	}

	movedKeySet := map[string]bool{}
	migratedGroups := map[int]bool{}
	for _, migration := range result.Migrations {
		if oldOwners[migration.Group] != migration.From || newOwners[migration.Group] != migration.To {
			t.Fatalf("migration %+v disagrees with ownership tables", migration)
		}
		if migration.From == migration.To {
			t.Fatalf("group %d migrated from %d to itself", migration.Group, migration.From)
		}
		if migration.MovedKeyNum != len(migration.MovedKeys) {
			t.Fatalf("group %d: MovedKeyNum=%d but %d keys", migration.Group, migration.MovedKeyNum, len(migration.MovedKeys))
		}
		migratedGroups[migration.Group] = true
		for _, key := range migration.MovedKeys {
			if movedKeySet[key] {
				t.Fatalf("key %q migrated twice", key)
			}
			if GroupOf(key, r.groupCount) != migration.Group {
				t.Fatalf("key %q migrated as member of wrong group", key)
			}
			movedKeySet[key] = true
		}
	}
	if !reflect.DeepEqual(migratedGroups, wantMovedGroups) {
		t.Fatalf("migrated groups = %v, want %v", migratedGroups, wantMovedGroups)
	}

	moveCount := 0
	for _, move := range result.Moves {
		group := GroupOf(move.Key, r.groupCount)
		if move.From != oldOwners[group] || move.To != newOwners[group] || move.From == move.To {
			t.Fatalf("invalid move %+v", move)
		}
		moveCount++
	}
	if moveCount != wantMovedKeys {
		t.Fatalf("moves = %d, want %d", moveCount, wantMovedKeys)
	}

	// Every key survives with its original value: no loss, no duplication.
	if len(result.States) != len(stored) {
		t.Fatalf("state size = %d after rescale, want %d", len(result.States), len(stored))
	}
	for key, want := range stored {
		got, ok := result.States[key]
		if !ok {
			t.Fatalf("key %q lost during rescale %d->%d", key, oldP, newP)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("key %q value = %q, want %q", key, got, want)
		}
	}
}
