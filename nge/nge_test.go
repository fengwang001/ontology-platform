package nge

import (
	"math/rand"
	"slices"
	"sync"
	"testing"
)

const testLimit = 1 << 20

func naive(a []int) []int {
	ans := make([]int, len(a))
	for i := range a {
		ans[i] = None
		for j := i + 1; j < len(a) && ans[i] == None; j++ {
			if a[j] > a[i] {
				ans[i] = j
			}
		}
	}
	return ans
}
func build(shape string, n int) []int {
	a := make([]int, n)
	for i := range a {
		a[i] = 7
		if shape == "inc" {
			a[i] = i
		}
		if shape == "dec" {
			a[i] = n - i
		}
	}
	return a
}
func TestNextGreaterAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	rnd := make([]int, 500)
	for i := range rnd {
		rnd[i] = r.Intn(50)
	}
	cases := []struct {
		name string
		a    []int
	}{
		{"two-two-three", []int{2, 2, 3}},
		{"strict increasing", build("inc", 300)},
		{"strict decreasing", build("dec", 300)},
		{"all equal", build("eq", 300)},
		{"random", rnd},
	}
	for _, tc := range cases {
		got, err := NextGreater(tc.a, testLimit)
		if err != nil || !slices.Equal(got, naive(tc.a)) || !SelfCheck(tc.a, got) {
			t.Errorf("%s: got=%v err=%v", tc.name, got, err)
		}
	}
}
func TestSelfCheckDetectsTampering(t *testing.T) {
	a := []int{2, 2, 3}
	good, err := NextGreater(a, testLimit)
	if err != nil || !SelfCheck(a, good) {
		t.Fatalf("valid answer rejected: %v %v", good, err)
	}
	cases := []struct {
		name string
		ans  []int
	}{
		{"equal is not greater", []int{1, 2, None}},
		{"false none", []int{None, 2, None}},
		{"self reference", []int{0, 2, None}},
		{"length mismatch", []int{2, 2}},
	}
	for _, tc := range cases {
		if SelfCheck(a, tc.ans) {
			t.Errorf("%s: tampered answer accepted", tc.name)
		}
	}
}
func TestConcurrentNextGreater(t *testing.T) {
	a := build("inc", 2000)
	want, _ := NextGreater(a, testLimit)
	gots := make([][]int, 16)
	wg, start := sync.WaitGroup{}, make(chan struct{})
	for k := range gots {
		wg.Add(1)
		go func(k int) { defer wg.Done(); <-start; gots[k], _ = NextGreater(a, testLimit) }(k)
	}
	close(start)
	wg.Wait()
	for k := range gots {
		if !slices.Equal(gots[k], want) {
			t.Errorf("goroutine %d: result differs", k)
		}
	}
}
