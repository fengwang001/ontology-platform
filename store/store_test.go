package store

import (
	"fmt"
	"sync"
	"testing"
)

func TestAtVersions(t *testing.T) {
	var bound uint64
	active := false
	s := New(func() (uint64, bool) { return bound, active })
	s.Put("a", []byte("v1"))
	v1 := 1
	active, bound = true, 1
	s.Put("a", []byte("v2")) // version 2; v1 retained for bound 1
	s.Put("b", []byte("b1")) // version 3
	cases := []struct {
		name string
		at   uint64
		key  string
		want string
		ok   bool
	}{
		{"oldest sees v1", uint64(v1), "a", "v1", true},
		{"latest sees v2", 3, "a", "v2", true},
		{"unknown key", 3, "z", "", false},
		{"b at v1 absent", uint64(v1), "b", "", false},
		{"b at v3 present", 3, "b", "b1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := s.At(c.key, c.at)
			if ok != c.ok || string(got) != c.want {
				t.Fatalf("At(%s,%d)=%q,%v want %q,%v", c.key, c.at, got, ok, c.want, c.ok)
			}
		})
	}
	if n := s.RetainedKeys(); n != 1 {
		t.Fatalf("retained keys = %d, want 1", n)
	}
	active = false
	s.Put("c", []byte("c1"))
	s.Prune()
	if n := s.RetainedKeys(); n != 0 {
		t.Fatalf("after all snapshots close retained = %d, want 0", n)
	}
}

func TestEmptyValueAndTombstone(t *testing.T) {
	s := New(nil)
	cases := []struct {
		name   string
		key    string
		value  []byte
		exists bool
	}{
		{"empty value is present", "k", []byte{}, true},
		{"nil value is absent", "d", nil, false},
	}
	for _, c := range cases {
		s.Put(c.key, c.value)
		got, ok := s.Latest(c.key)
		if ok != c.exists {
			t.Fatalf("%s: ok=%v want %v (value=%q)", c.name, ok, c.exists, got)
		}
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := New(nil)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s.Put(fmt.Sprintf("k%d", i), []byte{byte(g)})
			}
		}(g)
	}
	wg.Wait()
	if len(s.Keys(1_000_000)) != 200 {
		t.Fatal("unexpected key count after concurrent writes")
	}
}
