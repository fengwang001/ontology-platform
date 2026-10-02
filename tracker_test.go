package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func TestExampleSequence(t *testing.T) {
	tracker, err := NewTracker(4, []Edge{
		{From: 0, To: 1},
		{From: 1, To: 2, Delay: 1},
		{From: 2, To: 1, Delay: 1},
		{From: 2, To: 3},
	}, []int{0})
	if err != nil {
		t.Fatalf("NewTracker() error = %v", err)
	}

	steps := []struct {
		batch   []Delta
		wantVer uint64
		want    []FrontierChange
	}{
		{
			batch:   []Delta{{0, 5, 1}},
			wantVer: 1,
			want: []FrontierChange{
				{0, -1, 5}, {1, -1, 5}, {2, -1, 6}, {3, -1, 6},
			},
		},
		{
			batch:   []Delta{{0, 5, -1}, {1, 5, 1}},
			wantVer: 2,
			want:    []FrontierChange{{0, 5, -1}},
		},
		{
			batch:   []Delta{{1, 5, -1}, {2, 6, 1}},
			wantVer: 3,
			want:    []FrontierChange{{1, 5, 7}},
		},
	}

	for _, step := range steps {
		version, changes, rejection := tracker.Update(step.batch)
		if rejection.Reason != nil {
			t.Fatalf("Update(%v) rejected: %v", step.batch, rejection)
		}
		if version != step.wantVer || !reflect.DeepEqual(changes, step.want) {
			t.Fatalf("Update(%v) = (%d, %#v), want (%d, %#v)", step.batch, version, changes, step.wantVer, step.want)
		}
	}

	if _, frontiers := tracker.Frontiers(); !reflect.DeepEqual(frontiers, []int64{-1, 7, 6, 6}) {
		t.Fatalf("Frontiers() = %v", frontiers)
	}

	_, _, rejection := tracker.Update([]Delta{{3, 5, 1}})
	if !errors.Is(rejection.Reason, ErrCausalViolation) || rejection.Position != 3 || rejection.Time != 5 {
		t.Fatalf("causal rejection = %#v", rejection)
	}
	if version, _ := tracker.Frontiers(); version != 3 {
		t.Fatalf("rejected version = %d", version)
	}

	assertComplete := func(position int, time int64, want bool) {
		t.Helper()
		got, err := tracker.Complete(position, time)
		if err != nil || got != want {
			t.Fatalf("Complete(%d,%d) = (%v,%v), want %v", position, time, got, err, want)
		}
	}
	assertComplete(1, 6, true)
	assertComplete(1, 7, false)
	assertComplete(0, 1_000_000_000_000, true)
}
