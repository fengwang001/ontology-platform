package api_test

import (
	"strconv"
	"sync"
	"testing"

	"ontology/api"
)

func TestSelfCheck(t *testing.T) {
	s, err := api.New(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentRead: N goroutines reading a pre-filled instance must agree
// on every key's value and presence. Table-driven over goroutine counts.
func TestConcurrentRead(t *testing.T) {
	const keys = 32
	for _, n := range []int{4, 16, 64} {
		s, _ := api.New(8)
		for i := 0; i < keys; i++ {
			if err := s.Set("k"+strconv.Itoa(i), "v"+strconv.Itoa(i)); err != nil {
				t.Fatal(err)
			}
		}
		got := make([][]string, n)
		var wg sync.WaitGroup
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				row := make([]string, keys)
				for i := range row {
					v, ok := s.Read("k" + strconv.Itoa(i))
					if !ok {
						t.Errorf("key %d absent", i)
						return
					}
					row[i] = v
				}
				got[g] = row
			}(g)
		}
		wg.Wait()
		for g := 1; g < n; g++ {
			for i := 0; i < keys; i++ {
				if got[g][i] != got[0][i] {
					t.Fatalf("n=%d g=%d key %d: %q != %q", n, g, i, got[g][i], got[0][i])
				}
			}
		}
	}
}

// TestConcurrentWrite: N goroutines write disjoint keys; the final state must
// equal any serial interleaving (each key's last write wins within its owner).
func TestConcurrentWrite(t *testing.T) {
	for _, n := range []int{4, 16, 64} {
		s, _ := api.New(8)
		var wg sync.WaitGroup
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for j := 0; j < 50; j++ { // disjoint keys across goroutines
					if err := s.Set("w"+strconv.Itoa(g),
						"v"+strconv.Itoa(g)+"-"+strconv.Itoa(j)); err != nil {
						t.Error(err)
						return
					}
				}
			}(g)
		}
		wg.Wait()
		for g := 0; g < n; g++ {
			want := "v" + strconv.Itoa(g) + "-49"
			if v, ok := s.Read("w" + strconv.Itoa(g)); !ok || v != want {
				t.Fatalf("n=%d w%d=(%q,%v), want %q", n, g, v, ok, want)
			}
		}
	}
}

// TestConcurrentReadersMix: all four read-only entry points run concurrently.
func TestConcurrentReadersMix(t *testing.T) {
	s, _ := api.New(8)
	for i := 0; i < 64; i++ {
		if err := s.Set("k"+strconv.Itoa(i), "v"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			switch g % 4 {
			case 0:
				_, _ = s.Read("k" + strconv.Itoa(g))
			case 1:
				_ = s.BaseKeys()
			case 2:
				_ = s.DeltaLen()
			case 3:
				_ = s.SelfCheck()
			}
		}(g)
	}
	wg.Wait()
}
