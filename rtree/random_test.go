package rtree

import (
	"flag"
	"fmt"
	"math/rand"
	"testing"
)

func TestRandomSequences(t *testing.T) {
	verbose := flag.Lookup("test.v") != nil && flag.Lookup("test.v").Value.String() == "true"
	rng := rand.New(rand.NewSource(20261002))
	for iteration := 0; iteration < 2000; iteration++ {
		maxItems := 3 + rng.Intn(14)
		minItems := 1 + rng.Intn(maxItems/2)
		capacity := 1 + rng.Intn(80)
		tree, err := New(maxItems, minItems, capacity)
		if err != nil {
			t.Fatalf("case %d constructor: %v", iteration, err)
		}

		expected := make(map[int64]Rect)
		active := make([]int64, 0, capacity)
		steps := 2 + rng.Intn(80)
		if verbose || iteration < 5 {
			t.Logf("case=%d input M=%d m=%d C=%d steps=%d", iteration, maxItems, minItems, capacity, steps)
		}

		for step := 0; step < steps; step++ {
			roll := rng.Intn(10)
			switch {
			case roll < 6 || len(expected) == 0:
				id := int64(1 + rng.Intn(40))
				rect := randomRect(rng, 10)
				_, exists := expected[id]
				full := len(expected) >= capacity

				beforeCount := len(expected)
				beforeDump := tree.Dump()
				splits, insertErr := tree.Insert(id, rect)

				switch {
				case id <= 0 || !validRect(rect):
					t.Fatalf("case %d generated invalid insert", iteration)
				case exists:
					if insertErr != ErrDuplicateID {
						t.Fatalf("case %d step %d duplicate err=%v", iteration, step, insertErr)
					}
				case full:
					if insertErr != ErrCapacityFull {
						t.Fatalf("case %d step %d full err=%v", iteration, step, insertErr)
					}
				default:
					if insertErr != nil {
						t.Fatalf("case %d step %d insert: %v", iteration, step, insertErr)
					}
					expected[id] = rect
					active = appendActive(active, id)
				}
				logLine := fmt.Sprintf("case=%d step=%d input=Insert(%d,%v) output=splits=%d err=%v judgment=%s",
					iteration, step, id, rect, splits, insertErr,
					insertJudgment(exists, full, beforeCount, beforeDump, tree.Dump(), len(expected)))
				if verbose || iteration < 5 {
					t.Log(logLine)
				}

			case roll < 9:
				id := int64(1 + rng.Intn(40))
				_, exists := expected[id]
				removed, reinserted, deleteErr := tree.Delete(id)
				if exists {
					if deleteErr != nil {
						t.Fatalf("case %d step %d delete: %v", iteration, step, deleteErr)
					}
					delete(expected, id)
					logLine := fmt.Sprintf("case=%d step=%d input=Delete(%d) output=removed=%d reinserted=%d located=%d err=%v judgment=expected_deleted",
						iteration, step, id, removed, reinserted, tree.located.Load(), deleteErr)
					if verbose || iteration < 5 {
						t.Log(logLine)
					}
				} else {
					if deleteErr != ErrObjectNotFound {
						t.Fatalf("case %d step %d missing delete err=%v", iteration, step, deleteErr)
					}
					logLine := fmt.Sprintf("case=%d step=%d input=Delete(%d) output=removed=%d reinserted=%d located=%d err=%v judgment=expected_not_found",
						iteration, step, id, removed, reinserted, tree.located.Load(), deleteErr)
					if verbose || iteration < 5 {
						t.Log(logLine)
					}
				}

			default:
				query := randomRect(rng, 12)
				got, searchErr := tree.Search(query)
				if searchErr != nil {
					t.Fatalf("case %d step %d search: %v", iteration, step, searchErr)
				}
				want := bruteForce(expected, query)
				visited := tree.visited.Load()
				wantVisited := int64(1 + countVisitedChildren(tree.root, query))
				logLine := fmt.Sprintf("case=%d step=%d input=Search(%v) output=%v visited=%d judgment=bruteforce=%v visitedExpected=%d",
					iteration, step, query, got, visited, want, wantVisited)
				if verbose || iteration < 5 {
					t.Log(logLine)
				}
				if !sameIDs(got, want) {
					t.Fatalf("case %d step %d search mismatch dump=%s", iteration, step, tree.Dump())
				}
				if visited != wantVisited {
					t.Fatalf("case %d step %d visited=%d want=%d", iteration, step, visited, wantVisited)
				}
			}

			assertInvariants(t, tree, expected)
			dump := tree.Dump()
			if dump != tree.Dump() {
				t.Fatalf("case %d dump unstable", iteration)
			}
			_ = active
		}
		if verbose || iteration < 5 {
			t.Logf("case=%d output finalDump=%s finalObjects=%d judgment=invariants_pass", iteration, tree.Dump(), len(expected))
		}
	}
	t.Logf("output cases=2000 judgment=all_random_inserts_deletes_searches_match_bruteforce_and_invariants")
}

func randomRect(rng *rand.Rand, span int64) Rect {
	x1 := rng.Int63n(span) - 4
	y1 := rng.Int63n(span) - 4
	x2 := x1 + rng.Int63n(4)
	y2 := y1 + rng.Int63n(4)
	return Rect{X1: x1, Y1: y1, X2: x2, Y2: y2}
}

func appendActive(active []int64, id int64) []int64 {
	for _, current := range active {
		if current == id {
			return active
		}
	}
	return append(active, id)
}

func insertJudgment(exists bool, full bool, beforeCount int, beforeDump string, afterDump string, afterCount int) string {
	switch {
	case exists:
		return fmt.Sprintf("rejected_duplicate count_unchanged=%v dump_unchanged=%v", afterCount == beforeCount, afterDump == beforeDump)
	case full:
		return fmt.Sprintf("rejected_full count_unchanged=%v dump_unchanged=%v", afterCount == beforeCount, afterDump == beforeDump)
	default:
		return fmt.Sprintf("accepted count_increased=%v mbr_or_split_applied=%v", afterCount == beforeCount+1, true)
	}
}
