package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestRowsCurrentRowExcludesEarlierPeers(t *testing.T) {
	calculator, err := NewFrameCalculator(8)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []int64{5, 5, 5} {
		if err := calculator.Append(key); err != nil {
			t.Fatal(err)
		}
	}

	got, err := calculator.Frame(1, FrameSpec{
		Mode:    Rows,
		Start:   Bound{Type: CurrentRowBound},
		End:     Bound{Type: CurrentRowBound},
		Exclude: ExcludeNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Interval{{Start: 1, End: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRangeCurrentRowIncludesPeerGroup(t *testing.T) {
	calculator := mustCalculator(t, 8)
	for _, key := range []int64{5, 5, 5, 6} {
		mustAppend(t, calculator, key)
	}

	got := mustFrame(t, calculator, 1, FrameSpec{
		Mode:    Range,
		Start:   Bound{Type: CurrentRowBound},
		End:     Bound{Type: CurrentRowBound},
		Exclude: ExcludeNone,
	})
	want := []Interval{{Start: 0, End: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRangePrecedingIncludesExactKeyBoundary(t *testing.T) {
	calculator := mustCalculator(t, 16)
	keys := []int64{1, 2, 3, 3, 4, 6}
	for _, key := range keys {
		mustAppend(t, calculator, key)
	}

	got := mustFrame(t, calculator, 5, FrameSpec{
		Mode:    Range,
		Start:   Bound{Type: Preceding, N: 3},
		End:     Bound{Type: CurrentRowBound},
		Exclude: ExcludeNone,
	})
	want := []Interval{{Start: 2, End: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGroupsPrecedingToFollowing(t *testing.T) {
	calculator := mustCalculator(t, 16)
	for _, key := range []int64{1, 2, 2, 3, 3, 4} {
		mustAppend(t, calculator, key)
	}

	got := mustFrame(t, calculator, 2, FrameSpec{
		Mode:    Groups,
		Start:   Bound{Type: Preceding, N: 1},
		End:     Bound{Type: Following, N: 1},
		Exclude: ExcludeNone,
	})
	want := []Interval{{Start: 0, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestStartAfterEndProducesEmptyFrame(t *testing.T) {
	calculator := mustCalculator(t, 16)
	for row := 0; row < 10; row++ {
		mustAppend(t, calculator, int64(row))
	}

	got := mustFrame(t, calculator, 8, FrameSpec{
		Mode:    Rows,
		Start:   Bound{Type: Preceding, N: 3},
		End:     Bound{Type: Preceding, N: 5},
		Exclude: ExcludeNone,
	})
	if len(got) != 0 {
		t.Fatalf("got %v, want empty frame", got)
	}
}

func TestRangeExtremeOffsetsAvoidIntegerWraparound(t *testing.T) {
	calculator := mustCalculator(t, 4)
	keys := []int64{-1 << 62, 0, 1 << 62, 1<<62 + 7}
	for _, key := range keys {
		mustAppend(t, calculator, key)
	}

	got := mustFrame(t, calculator, 0, FrameSpec{
		Mode:    Range,
		Start:   Bound{Type: CurrentRowBound},
		End:     Bound{Type: Following, N: 1<<63 - 1},
		Exclude: ExcludeNone,
	})
	want := []Interval{{Start: 0, End: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExclusionsSplitFrame(t *testing.T) {
	calculator := mustCalculator(t, 16)
	for _, key := range []int64{1, 2, 2, 2, 3} {
		mustAppend(t, calculator, key)
	}
	spec := FrameSpec{
		Mode:    Range,
		Start:   Bound{Type: UnboundedPreceding},
		End:     Bound{Type: UnboundedFollowing},
		Exclude: ExcludeTies,
	}

	ties := mustFrame(t, calculator, 2, spec)
	wantTies := []Interval{{Start: 0, End: 1}, {Start: 2, End: 3}, {Start: 4, End: 5}}
	if !reflect.DeepEqual(ties, wantTies) {
		t.Fatalf("ties got %v, want %v", ties, wantTies)
	}

	spec.Exclude = ExcludeGroup
	group := mustFrame(t, calculator, 2, spec)
	wantGroup := []Interval{{Start: 0, End: 1}, {Start: 4, End: 5}}
	if !reflect.DeepEqual(group, wantGroup) {
		t.Fatalf("group got %v, want %v", group, wantGroup)
	}
}

func TestValidationAndRejectedAppendOrder(t *testing.T) {
	if _, err := NewFrameCalculator(0); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("capacity error = %v", err)
	}

	calculator := mustCalculator(t, 1)
	mustAppend(t, calculator, 5)
	if err := calculator.Append(4); !errors.Is(err, ErrPartitionFull) {
		t.Fatalf("full append error = %v", err)
	}

	calculator.capacity = 2
	if err := calculator.Append(4); !errors.Is(err, ErrKeyOutOfOrder) {
		t.Fatalf("out-of-order append error = %v", err)
	}
	if got := len(calculator.keys); got != 1 {
		t.Fatalf("rejected append changed length to %d", got)
	}

	tests := []struct {
		name string
		spec FrameSpec
		want error
	}{
		{
			name: "invalid enum",
			spec: FrameSpec{Mode: Mode(9), Start: unboundedPrecedingBound(), End: unboundedFollowingBound()},
			want: ErrInvalidFrameSpec,
		},
		{
			name: "invalid direction",
			spec: FrameSpec{Mode: Rows, Start: unboundedFollowingBound(), End: unboundedFollowingBound()},
			want: ErrInvalidFrameOrder,
		},
		{
			name: "negative offset",
			spec: FrameSpec{
				Mode:  Rows,
				Start: Bound{Type: Preceding, N: -1},
				End:   unboundedFollowingBound(),
			},
			want: ErrInvalidOffset,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := calculator.Frame(0, test.spec); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	if _, err := calculator.Frame(1, validSpec()); !errors.Is(err, ErrRowIndexOutOfRange) {
		t.Fatalf("index error = %v", err)
	}
}

func TestFrameResultDoesNotShareInternalStorage(t *testing.T) {
	calculator := mustCalculator(t, 8)
	mustAppend(t, calculator, 1)
	mustAppend(t, calculator, 1)
	got := mustFrame(t, calculator, 0, validSpec())
	got[0].Start = 99

	again := mustFrame(t, calculator, 0, validSpec())
	want := []Interval{{Start: 0, End: 2}}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("internal storage changed: got %v, want %v", again, want)
	}
}

func TestConcurrentAppendAndFrame(t *testing.T) {
	calculator := mustCalculator(t, 128)
	for row := 0; row < 4; row++ {
		mustAppend(t, calculator, int64(row))
	}

	var waitGroup sync.WaitGroup
	spec := FrameSpec{
		Mode:    Rows,
		Start:   Bound{Type: UnboundedPreceding},
		End:     Bound{Type: UnboundedFollowing},
		Exclude: ExcludeNone,
	}

	for worker := 0; worker < 4; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for step := 0; step < 32; step++ {
				if step%2 == worker%2 {
					_ = calculator.Append(int64(4 + worker*32 + step))
				} else {
					_, _ = calculator.Frame(step%4, spec)
				}
			}
		}(worker)
	}
	waitGroup.Wait()
}

func mustCalculator(t *testing.T, capacity int) *FrameCalculator {
	t.Helper()
	calculator, err := NewFrameCalculator(capacity)
	if err != nil {
		t.Fatal(err)
	}
	return calculator
}

func mustAppend(t *testing.T, calculator *FrameCalculator, key int64) {
	t.Helper()
	if err := calculator.Append(key); err != nil {
		t.Fatal(err)
	}
}

func mustFrame(t *testing.T, calculator *FrameCalculator, i int, spec FrameSpec) []Interval {
	t.Helper()
	got, err := calculator.Frame(i, spec)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func validSpec() FrameSpec {
	return FrameSpec{
		Mode:    Range,
		Start:   unboundedPrecedingBound(),
		End:     unboundedFollowingBound(),
		Exclude: ExcludeNone,
	}
}

func unboundedPrecedingBound() Bound {
	return Bound{Type: UnboundedPreceding}
}

func unboundedFollowingBound() Bound {
	return Bound{Type: UnboundedFollowing}
}
