package bplus

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestNamedRebalanceRules(t *testing.T) {
	cases := []struct {
		name             string
		leafCapacity     int
		internalCapacity int
		fillPercent      int
		n                int
		wantLeaves       []int
	}{
		{"last exactly minimum", 5, 4, 50, 5, []int{3, 2}},
		{"last one below minimum redistributes", 6, 5, 75, 7, []int{4, 3}},
		{"odd total previous gets extra", 10, 5, 70, 11, []int{6, 5}},
		{"pair merges when total fits", 4, 4, 1, 3, []int{3}},
		{"fill percent 100", 6, 4, 100, 7, []int{4, 3}},
		{"fill percent 1 raised to minimum", 8, 4, 1, 5, []int{5}},
		{"single leaf root", 4, 4, 50, 3, []int{3}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loader, err := NewBulkLoader(tc.leafCapacity, tc.internalCapacity, tc.fillPercent)
			if err != nil {
				t.Fatal(err)
			}
			if err := loader.Add(testKeys(tc.n)...); err != nil {
				t.Fatal(err)
			}
			if err := loader.Finish(); err != nil {
				t.Fatal(err)
			}
			got := levelSizes(loader.Pages(), 0)
			t.Logf("case=%s input=%v output=%v basis=%v", tc.name, testKeys(tc.n), got, tc.wantLeaves)
			if fmt.Sprint(got) != fmt.Sprint(tc.wantLeaves) {
				t.Fatalf("leaf occupancies=%v want %v", got, tc.wantLeaves)
			}
		})
	}
}

func TestInternalLastGroupRebalance(t *testing.T) {
	loader, err := NewBulkLoader(4, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Add(testKeys(17)...); err != nil {
		t.Fatal(err)
	}
	if err := loader.Finish(); err != nil {
		t.Fatal(err)
	}
	pages := loader.Pages()
	got := occupancies(pages)
	want := [][]int{{4, 4, 4, 3, 2}, {3, 2}, {2}}
	t.Logf("input=%v output=%v basis=%v", testKeys(17), got, want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("occupancies=%v want %v", got, want)
	}
}

func TestAddBatchingProducesSamePages(t *testing.T) {
	keys := testKeys(37)
	reference := finishedPages(t, 4, 5, 75, keys)
	chunks := []int{1, 2, 3, 5, 8, 37}
	for _, chunk := range chunks {
		loader, err := NewBulkLoader(4, 5, 75)
		if err != nil {
			t.Fatal(err)
		}
		for start := 0; start < len(keys); start += chunk {
			end := min(start+chunk, len(keys))
			if err := loader.Add(keys[start:end]...); err != nil {
				t.Fatal(err)
			}
		}
		if err := loader.Finish(); err != nil {
			t.Fatal(err)
		}
		got := loader.Pages()
		t.Logf("chunk=%d output=%v basis=same keys must produce same pages", chunk, occupancies(got))
		if fmt.Sprint(got) != fmt.Sprint(reference) {
			t.Fatalf("chunk %d produced different pages", chunk)
		}
	}
}

func TestRejectedOperationsDoNotMutate(t *testing.T) {
	t.Run("constructor order", func(t *testing.T) {
		if _, err := NewBulkLoader(1, 2, 0); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("err=%v", err)
		}
		if _, err := NewBulkLoader(2, 2, 0); !errors.Is(err, ErrInvalidDegree) {
			t.Fatalf("err=%v", err)
		}
		if _, err := NewBulkLoader(2, 3, 0); !errors.Is(err, ErrInvalidPercent) {
			t.Fatalf("err=%v", err)
		}
	})

	loader, err := NewBulkLoader(4, 4, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Add("b", "", "a"); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty-key priority err=%v", err)
	}
	if err := loader.Add("b"); err != nil {
		t.Fatal(err)
	}
	err = loader.Add("b", "a")
	var orderErr OrderError
	if !errors.As(err, &orderErr) || orderErr.Index != 1 || orderErr.Key != "b" {
		t.Fatalf("order error=%#v", err)
	}
	if err := loader.Add("a"); err == nil || !errors.Is(err, ErrKeyOrder) {
		t.Fatalf("global order err=%v", err)
	}
	if _, err := loader.Get("a"); !errors.Is(err, ErrNotFinished) {
		t.Fatalf("Get before Finish err=%v", err)
	}
	if err := loader.Add("c"); err != nil {
		t.Fatal(err)
	}
	if err := loader.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := loader.Add("d"); !errors.Is(err, ErrFinished) {
		t.Fatalf("Add after Finish err=%v", err)
	}
	if err := loader.Finish(); !errors.Is(err, ErrFinished) {
		t.Fatalf("second Finish err=%v", err)
	}

	pages := loader.Pages()
	t.Logf("output=%v basis=rejected operations must not alter accepted keys", flattenKeys(pages))
	if got := flattenKeys(pages); fmt.Sprint(got) != "[b c]" {
		t.Fatalf("stored keys=%v", got)
	}
}

func TestConcurrentAddFinishGet(t *testing.T) {
	loader, err := NewBulkLoader(10, 5, 50)
	if err != nil {
		t.Fatal(err)
	}
	const total = 120
	start := make(chan struct{})
	var readers sync.WaitGroup
	for reader := 0; reader < 3; reader++ {
		readers.Add(1)
		go func(reader int) {
			defer readers.Done()
			<-start
			key := fmt.Sprintf("%03d", reader*10)
			for {
				result, getErr := loader.Get(key)
				if getErr == nil {
					if result.Accesses != loader.Height() {
						t.Errorf("Get accesses=%d height=%d", result.Accesses, loader.Height())
					}
					return
				}
				if !errors.Is(getErr, ErrNotFinished) {
					t.Errorf("Get err=%v", getErr)
					return
				}
			}
		}(reader)
	}

	finishDone := make(chan struct{})
	go func() {
		<-start
		_ = loader.Finish()
		close(finishDone)
	}()

	var adder sync.WaitGroup
	adder.Add(1)
	go func() {
		defer adder.Done()
		<-start
		for index := 0; index < total; index++ {
			if addErr := loader.Add(fmt.Sprintf("%03d", index)); addErr != nil {
				if !errors.Is(addErr, ErrFinished) {
					t.Errorf("Add err=%v", addErr)
				}
				return
			}
		}
	}()

	close(start)
	adder.Wait()
	<-finishDone
	readers.Wait()

	pages := loader.Pages()
	keys := flattenKeys(pages)
	want := testKeys(len(keys))
	t.Logf("output=%v basis=racing Add/Finish must equal either Finish-first or an accepted increasing prefix", occupancies(pages))
	if fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Fatalf("accepted keys are not an increasing prefix: %v", keys)
	}
	assertOccupancy(t, pages, 10, 5, 5, 3)
	for _, key := range keys {
		result, getErr := loader.Get(key)
		if getErr != nil || !result.Found || result.Accesses != len(pages) {
			t.Fatalf("Get(%q)=%+v,%v", key, result, getErr)
		}
	}
}

func finishedPages(t *testing.T, leafCapacity, internalCapacity, fillPercent int, keys []string) [][]Page {
	t.Helper()
	loader, err := NewBulkLoader(leafCapacity, internalCapacity, fillPercent)
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Add(keys...); err != nil {
		t.Fatal(err)
	}
	if err := loader.Finish(); err != nil {
		t.Fatal(err)
	}
	return loader.Pages()
}

func levelSizes(levels [][]Page, level int) []int {
	var sizes []int
	for _, page := range levels[level] {
		size := len(page.Keys)
		if level > 0 {
			size = len(page.Children)
		}
		sizes = append(sizes, size)
	}
	return sizes
}

func flattenKeys(levels [][]Page) []string {
	var keys []string
	for _, page := range levels[0] {
		keys = append(keys, page.Keys...)
	}
	return keys
}
