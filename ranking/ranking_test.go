package ranking

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestRankingRules(t *testing.T) {
	maintainer := NewMaintainer()
	insertions := []struct {
		id    string
		score int
		want  Entry
	}{
		{"c", 90, Entry{ID: "c", Score: 90, Position: 1, Rank: 1, DenseRank: 1}},
		{"a", 90, Entry{ID: "a", Score: 90, Position: 1, Rank: 1, DenseRank: 1}},
		{"b", 90, Entry{ID: "b", Score: 90, Position: 2, Rank: 1, DenseRank: 1}},
		{"d", 80, Entry{ID: "d", Score: 80, Position: 4, Rank: 4, DenseRank: 2}},
		{"e", 80, Entry{ID: "e", Score: 80, Position: 5, Rank: 4, DenseRank: 2}},
		{"f", 70, Entry{ID: "f", Score: 70, Position: 6, Rank: 6, DenseRank: 3}},
	}

	for _, input := range insertions {
		got, err := maintainer.Insert(input.id, input.score)
		if err != nil {
			t.Fatalf("Insert(%q, %d) returned error: %v", input.id, input.score, err)
		}
		if got != input.want {
			t.Fatalf("Insert(%q, %d) = %+v, want %+v", input.id, input.score, got, input.want)
		}
		t.Logf("input=Insert id=%q score=%d result=%+v basis=score-desc/id-asc, ties share rank, gaps count people, dense rank counts scores", input.id, input.score, got)
	}

	wantList := []Entry{
		{ID: "a", Score: 90, Position: 1, Rank: 1, DenseRank: 1},
		{ID: "b", Score: 90, Position: 2, Rank: 1, DenseRank: 1},
		{ID: "c", Score: 90, Position: 3, Rank: 1, DenseRank: 1},
		{ID: "d", Score: 80, Position: 4, Rank: 4, DenseRank: 2},
		{ID: "e", Score: 80, Position: 5, Rank: 4, DenseRank: 2},
		{ID: "f", Score: 70, Position: 6, Rank: 6, DenseRank: 3},
	}
	gotSnapshot := maintainer.List()
	assertEntries(t, gotSnapshot.Entries, wantList)
	t.Logf("input=List result=%+v basis=positions 1..6 are unique and ranks=%v dense=%v", gotSnapshot.Entries, ranks(gotSnapshot.Entries), denseRanks(gotSnapshot.Entries))

	if err := maintainer.Delete("d"); err != nil {
		t.Fatalf("Delete(%q) returned error: %v", "d", err)
	}
	wantAfterDelete := []Entry{
		{ID: "a", Score: 90, Position: 1, Rank: 1, DenseRank: 1},
		{ID: "b", Score: 90, Position: 2, Rank: 1, DenseRank: 1},
		{ID: "c", Score: 90, Position: 3, Rank: 1, DenseRank: 1},
		{ID: "e", Score: 80, Position: 4, Rank: 4, DenseRank: 2},
		{ID: "f", Score: 70, Position: 5, Rank: 5, DenseRank: 3},
	}
	assertEntries(t, maintainer.List().Entries, wantAfterDelete)
	t.Logf("input=Delete id=%q result=%+v basis=lower element position shifts, tied score keeps rank 4 and dense score 80 remains", "d", wantAfterDelete)

	if err := maintainer.Delete("e"); err != nil {
		t.Fatalf("Delete(%q) returned error: %v", "e", err)
	}
	wantAfterLastScore := []Entry{
		{ID: "a", Score: 90, Position: 1, Rank: 1, DenseRank: 1},
		{ID: "b", Score: 90, Position: 2, Rank: 1, DenseRank: 1},
		{ID: "c", Score: 90, Position: 3, Rank: 1, DenseRank: 1},
		{ID: "f", Score: 70, Position: 4, Rank: 4, DenseRank: 2},
	}
	assertEntries(t, maintainer.List().Entries, wantAfterLastScore)
	t.Logf("input=Delete id=%q result=%+v basis=last score-80 element removed, dense rank for 70 drops from 3 to 2 and rank drops from 5 to 4", "e", wantAfterLastScore)

	report := maintainer.SelfCheck()
	if !report.OK {
		t.Fatalf("SelfCheck failed: %s", report.Reason)
	}
	t.Logf("input=SelfCheck result=%+v basis=order, index mapping, position bijection, ranks and dense ranks all verified", report)
}

func TestInvalidOperationsDoNotMutateState(t *testing.T) {
	maintainer := NewMaintainer()
	if _, err := maintainer.Insert("a", 10); err != nil {
		t.Fatal(err)
	}
	before := maintainer.List()

	tests := []struct {
		name      string
		operation func() error
		wantErr   error
		input     string
	}{
		{
			name:      "insert empty id",
			operation: func() error { _, err := maintainer.Insert("", 10); return err },
			wantErr:   ErrEmptyID,
			input:     "",
		},
		{
			name:      "insert duplicate id",
			operation: func() error { _, err := maintainer.Insert("a", 20); return err },
			wantErr:   ErrDuplicateID,
			input:     "a",
		},
		{
			name:      "delete empty id",
			operation: func() error { return maintainer.Delete("") },
			wantErr:   ErrEmptyID,
			input:     "",
		},
		{
			name:      "delete missing id",
			operation: func() error { return maintainer.Delete("missing") },
			wantErr:   ErrIDNotFound,
			input:     "missing",
		},
	}

	for _, tt := range tests {
		err := tt.operation()
		if !errors.Is(err, tt.wantErr) {
			t.Fatalf("%s returned error %v, want %v", tt.name, err, tt.wantErr)
		}
		after := maintainer.List()
		assertEntries(t, after.Entries, before.Entries)
		t.Logf("input=%s id=%q result=%v state=%+v basis=rejected operation takes no effect and error is decidable with errors.Is", tt.name, tt.input, err, after.Entries)
	}
}

func TestZeroMaintainerIsUsable(t *testing.T) {
	var maintainer Maintainer
	entry, err := maintainer.Insert("a", 10)
	if err != nil {
		t.Fatalf("Insert on zero maintainer returned error: %v", err)
	}
	want := Entry{ID: "a", Score: 10, Position: 1, Rank: 1, DenseRank: 1}
	if entry != want {
		t.Fatalf("Insert on zero maintainer = %+v, want %+v", entry, want)
	}
	if report := maintainer.SelfCheck(); !report.OK {
		t.Fatalf("SelfCheck on zero maintainer failed: %s", report.Reason)
	}
	t.Logf("input=Insert zero-maintainer id=%q score=%d result=%+v basis=internal index is initialized lazily", "a", 10, entry)
}

func TestConcurrentInsertsQueriesAndChecks(t *testing.T) {
	const elementCount = 200
	maintainer := NewMaintainer()
	var workers sync.WaitGroup

	start := make(chan struct{})
	for i := 0; i < elementCount; i++ {
		i := i
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			id := fmt.Sprintf("%03d", i)
			entry, err := maintainer.Insert(id, i%5)
			if err != nil {
				t.Errorf("Insert(%q, %d) returned error: %v", id, i%5, err)
				return
			}
			t.Logf("input=Insert id=%q score=%d result=%+v basis=unique id accepts exactly once", id, i%5, entry)
		}()
	}

	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			<-start
			for i := 0; i < 100; i++ {
				snapshot := maintainer.List()
				if !nonDecreasingRanks(snapshot.Entries) {
					t.Errorf("reader %d observed ranks that are not non-decreasing: %+v", worker, ranks(snapshot.Entries))
					return
				}
				if !distinctPositions(snapshot.Entries) {
					t.Errorf("reader %d observed duplicate positions: %+v", worker, positions(snapshot.Entries))
					return
				}
				report := maintainer.SelfCheck()
				if !report.OK {
					t.Errorf("reader %d self-check failed: %s", worker, report.Reason)
					return
				}
			}
			t.Logf("input=List/SelfCheck reader=%d result=ok basis=observed sorted ranks non-decreasing and positions distinct", worker)
		}(worker)
	}

	close(start)
	workers.Wait()

	snapshot := maintainer.List()
	if len(snapshot.Entries) != elementCount {
		t.Fatalf("List has %d entries, want %d", len(snapshot.Entries), elementCount)
	}
	if !positionsFormBijection(snapshot.Entries, elementCount) {
		t.Fatalf("positions are not a 1..%d bijection: %v", elementCount, positions(snapshot.Entries))
	}
	if !nonDecreasingRanks(snapshot.Entries) {
		t.Fatalf("final ranks are not non-decreasing: %v", ranks(snapshot.Entries))
	}
	report := maintainer.SelfCheck()
	if !report.OK {
		t.Fatalf("final SelfCheck failed: %s", report.Reason)
	}
	t.Logf("input=List/SelfCheck final result=entries=%d positions=%v check=%+v basis=all inserts completed and positions form a 1..N bijection", elementCount, positions(snapshot.Entries), report)
}

func assertEntries(t *testing.T, got, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func positions(entries []Entry) []int {
	result := make([]int, len(entries))
	for i, entry := range entries {
		result[i] = entry.Position
	}
	return result
}

func ranks(entries []Entry) []int {
	result := make([]int, len(entries))
	for i, entry := range entries {
		result[i] = entry.Rank
	}
	return result
}

func denseRanks(entries []Entry) []int {
	result := make([]int, len(entries))
	for i, entry := range entries {
		result[i] = entry.DenseRank
	}
	return result
}

func distinctPositions(entries []Entry) bool {
	seen := make(map[int]bool, len(entries))
	for _, entry := range entries {
		if seen[entry.Position] {
			return false
		}
		seen[entry.Position] = true
	}
	return true
}

func positionsFormBijection(entries []Entry, count int) bool {
	if len(entries) != count || !distinctPositions(entries) {
		return false
	}
	seen := make(map[int]bool, count)
	for _, entry := range entries {
		if entry.Position < 1 || entry.Position > count || seen[entry.Position] {
			return false
		}
		seen[entry.Position] = true
	}
	return len(seen) == count
}

func nonDecreasingRanks(entries []Entry) bool {
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Rank > entries[i].Rank {
			return false
		}
	}
	return true
}
