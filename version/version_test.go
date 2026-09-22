package version

import "testing"

func TestZeroMeansNoVersion(t *testing.T) {
	var v Version
	if !v.IsZero() || v != None {
		t.Fatalf("zero value must be None, got %v", v)
	}
	if v.String() != "none" {
		t.Fatalf("zero String = %q", v.String())
	}
}

func TestComparison(t *testing.T) {
	v1, v2 := Version(1), Version(2)
	if !v1.Before(v2) || !v2.After(v1) || !v1.Equal(v1) {
		t.Fatal("ordering broken")
	}
	if None.Before(v1) != true {
		t.Fatal("None must be older than any allocated version")
	}
	if v2.Before(v1) || v1.After(v2) || v1.Equal(v2) {
		t.Fatal("ordering broken")
	}
}

func TestAllocatorMonotonic(t *testing.T) {
	a := NewAllocator()
	if got := a.Current(); got != None {
		t.Fatalf("fresh allocator Current = %v", got)
	}
	prev := None
	for i := 0; i < 1000; i++ {
		v := a.Next()
		if !v.After(prev) {
			t.Fatalf("not monotonic at %d: %v after %v", i, v, prev)
		}
		prev = v
	}
	if a.Current() != prev {
		t.Fatalf("Current = %v, want %v", a.Current(), prev)
	}
}

func TestAllocatorConcurrent(t *testing.T) {
	a := NewAllocator()
	seen := make(chan Version, 200)
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 10; j++ {
				seen <- a.Next()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	close(seen)
	set := map[Version]bool{}
	for v := range seen {
		if set[v] {
			t.Fatalf("duplicate version %v", v)
		}
		set[v] = true
	}
	if len(set) != 200 {
		t.Fatalf("got %d distinct versions, want 200", len(set))
	}
}
