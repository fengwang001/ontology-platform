package check_test

import (
	"errors"
	"slices"
	"testing"

	"ontology/check"
	"ontology/rng"
	"ontology/shuffle"
)

func wrongShuffle[T any](arr []T, seed uint64) {
	random := rng.New(seed)
	for i := 0; i < len(arr)-1; i++ {
		j := random.Intn(len(arr))
		arr[i], arr[j] = arr[j], arr[i]
	}
}

func TestShuffleCases(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(t *testing.T)
	}{
		{"deterministic", func(t *testing.T) {
			a, b := []int{1, 2, 3, 4}, []int{1, 2, 3, 4}
			shuffle.Shuffle(a, 42)
			shuffle.Shuffle(b, 42)
			if !slices.Equal(a, b) {
				t.Fatal("same seed produced different results")
			}
		}},
		{"empty and single", func(t *testing.T) {
			empty, single := []int{}, []int{7}
			shuffle.Shuffle(empty, 1)
			shuffle.Shuffle(single, 1)
			if len(empty) != 0 || !slices.Equal(single, []int{7}) {
				t.Fatal("boundary case changed")
			}
		}},
		{"multiset", func(t *testing.T) {
			if err := check.VerifyPermutation([]int{1, 2, 2, 3}, 9); err != nil {
				t.Fatal(err)
			}
		}},
		{"random calls", func(t *testing.T) {
			before := shuffle.RandomCalls()
			shuffle.Shuffle([]int{1, 2, 3, 4}, 10)
			if got := shuffle.RandomCalls() - before; got != 3 {
				t.Fatalf("calls=%d, want 3", got)
			}
		}},
		{"n2 balance", func(t *testing.T) {
			if ratio := check.CountRatio(check.Distribution(2, 120000, 1)); ratio > 1.1 {
				t.Fatalf("ratio=%f", ratio)
			}
		}},
		{"uniform n4", func(t *testing.T) {
			if err := check.VerifyUniform(4, 120000, 1, 1.5); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong full range", func(t *testing.T) {
			counts := map[string]int{}
			for seed := uint64(0); seed < 256; seed++ {
				arr := []byte("ABCD")
				wrongShuffle(arr, seed)
				counts[string(arr)]++
			}
			if ratio := check.CountRatio(counts); ratio <= 5 {
				t.Fatalf("ratio=%f, want > 5", ratio)
			}
		}},
		{"sentinel errors", func(t *testing.T) {
			errs := []error{
				check.VerifyUniform(0, 1, 1, 1),
				check.VerifyMultiset([]int{1}, []int{1, 2}),
				func() error {
					bad := check.Distribution(4, 1, 1)
					bad["WXYZ"] = 1
					if check.CountRatio(bad) > 1.5 {
						return check.ErrNotUniform
					}
					return nil
				}(),
			}
			want := []error{check.ErrInvalidInput, check.ErrNotPermutation, check.ErrNotUniform}
			for i, err := range errs {
				if !errors.Is(err, want[i]) {
					t.Fatalf("case %d: %v", i, err)
				}
			}
		}},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestConcurrentDistinctArrays(t *testing.T) {
	done := make(chan struct{}, 16)
	for i := 0; i < 16; i++ {
		go func(seed uint64) {
			shuffle.Shuffle([]int{1, 2, 3, 4, 5, 6, 7, 8}, seed)
			done <- struct{}{}
		}(uint64(i))
	}
	for i := 0; i < 16; i++ {
		<-done
	}
}
