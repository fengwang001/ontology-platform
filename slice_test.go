package coercion

import (
	"errors"
	"reflect"
	"testing"
)

func TestEmptySliceIsPresentAndDistinctFromNil(t *testing.T) {
	converter := New(Strict)
	target := Target{Kind: Int64Slice, Nullable: true}

	empty, err := converter.Convert([]int64{}, target)
	if err != nil || !empty.IsPresent() || empty.IsNull() {
		t.Fatalf("empty = %#v, %v", empty, err)
	}
	if got := empty.Value.([]int64); len(got) != 0 || got == nil {
		t.Fatalf("empty value = %#v", got)
	}

	nilSlice, err := converter.Convert([]int64(nil), target)
	if err != nil || !nilSlice.IsNull() || nilSlice.IsPresent() || nilSlice.Value != nil {
		t.Fatalf("nil = %#v, %v", nilSlice, err)
	}
}

func TestSliceFailureCarriesZeroBasedIndexAndCategory(t *testing.T) {
	input := []any{int64(1), "bad", int64(3)}
	_, err := New(Strict).Convert(input, Target{Kind: Int64Slice})
	var errs Errors
	if !errors.As(err, &errs) || len(errs) != 1 {
		t.Fatalf("err = %#v", err)
	}
	failure := errs[0]
	if failure.Index != 1 || !failure.hasIndex || failure.Category != Invalid {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestLenientSliceSkipsFailedElementAndRecordsIt(t *testing.T) {
	input := []any{int64(1), "bad", 2.5, int64(4)}
	result, err := New(Lenient).Convert(input, Target{Kind: Int64Slice})
	if err != nil {
		t.Fatalf("lenient slice failed: %v", err)
	}
	want := []int64{1, 2, 4}
	if got := result.Value.([]int64); !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
	if len(result.Degradations) != 2 {
		t.Fatalf("degradations = %#v", result.Degradations)
	}
	if result.Degradations[0].Index != 1 || !result.Degradations[0].Skipped ||
		result.Degradations[0].Category != Invalid {
		t.Fatalf("invalid record = %#v", result.Degradations[0])
	}
	if result.Degradations[1].Index != 2 || result.Degradations[1].Skipped ||
		result.Degradations[1].Category != PrecisionLoss {
		t.Fatalf("precision record = %#v", result.Degradations[1])
	}
}

func TestNonNilableNilSliceIsNullError(t *testing.T) {
	_, err := New(Strict).Convert([]string(nil), Target{Kind: StringSlice})
	category, ok := CategoryOf(err)
	if !ok || category != Null {
		t.Fatalf("category = %q, err = %v", category, err)
	}
}

func TestSliceElementTypeIsReported(t *testing.T) {
	_, err := New(Strict).Convert([]any{true}, Target{Kind: Int64Slice})
	var errs Errors
	if !errors.As(err, &errs) || errs[0].Target != Int64Kind {
		t.Fatalf("err = %#v", err)
	}
}

func TestTypedSlicesAndArraysAreAccepted(t *testing.T) {
	result, err := New(Strict).Convert([2]float64{1, 2}, Target{Kind: Float64Slice})
	if err != nil || !reflect.DeepEqual(result.Value, []float64{1, 2}) {
		t.Fatalf("array result = %#v, %v", result, err)
	}
}
