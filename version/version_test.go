package version

import (
	"sync"
	"testing"
)

func TestZeroMeansNoVersion(t *testing.T) {
	var v Version
	if !v.IsZero() || v != None {
		t.Fatalf("zero value should be None, got %v", v)
	}
	if !Version(1).After(None) {
		t.Fatal("any non-zero version must be newer than None")
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b Version
		want int
	}{
		{1, 2, -1},
		{2, 1, 1},
		{3, 3, 0},
		{0, 1, -1},
		{0, 0, 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Fatalf("Compare(%d,%d)=%d want %d", c.a, c.b, got, c.want)
		}
	}
	if !Version(5).After(Version(4)) || !Version(4).Before(Version(5)) {
		t.Fatal("After/Before inconsistent")
	}
	if !Version(5).Equal(Version(5)) || Version(5).Equal(Version(6)) {
		t.Fatal("Equal inconsistent")
	}
	if Max(3, 7) != 7 || Max(9, 2) != 9 {
		t.Fatal("Max inconsistent")
	}
}

func TestAllocatorMonotonic(t *testing.T) {
	a := NewAllocator()
	prev := None
	for i := 0; i < 1000; i++ {
		v := a.Next()
		if !v.After(prev) {
			t.Fatalf("not monotonic: %v after %v", v, prev)
		}
		prev = v
	}
}

func TestAllocatorConcurrentUnique(t *testing.T) {
	a := NewAllocator()
	const n = 64
	results := make([]Version, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = a.Next()
		}(i)
	}
	wg.Wait()
	seen := make(map[Version]bool)
	for _, v := range results {
		if v.IsZero() || seen[v] {
			t.Fatalf("duplicate or zero version %v", v)
		}
		seen[v] = true
	}
}
