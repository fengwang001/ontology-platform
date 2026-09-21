package walstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentCommitRace 在 -race 下并发提交，同时并发 Get/Len。
func TestConcurrentCommitRace(t *testing.T) {
	s, dir := openTempStore(t)

	const goroutines = 16
	const perG = 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				batch := map[string]string{
					fmt.Sprintf("g%d-k%d-a", g, i): fmt.Sprintf("v%d-%d-a", g, i),
					fmt.Sprintf("g%d-k%d-b", g, i): fmt.Sprintf("v%d-%d-b", g, i),
					fmt.Sprintf("g%d-k%d-c", g, i): "",
				}
				if err := s.Commit(batch); err != nil {
					t.Errorf("commit g%d i%d: %v", g, i, err)
					return
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < goroutines*perG*2; i++ {
			s.Get(fmt.Sprintf("g%d-k%d-a", i%goroutines, (i/2)%perG))
			s.Len()
		}
	}()
	wg.Wait()
	if want := goroutines * perG * 3; s.Len() != want {
		t.Fatalf("Len = %d, want %d", s.Len(), want)
	}
	s.Close()

	// 干净重启：每个已确认批次都完整可见。
	got := reopen(t, dir)
	defer got.Close()
	if got.Len() != goroutines*perG*3 {
		t.Fatalf("after restart Len = %d", got.Len())
	}
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perG; i++ {
			if v, ok := got.Get(fmt.Sprintf("g%d-k%d-a", g, i)); !ok || v != fmt.Sprintf("v%d-%d-a", g, i) {
				t.Fatalf("batch g%d i%d torn: %q,%v", g, i, v, ok)
			}
		}
	}

	// 再做一次"崩溃 + 任意截断"：可见批次必须完整（是已成功批次的某个超/子集，
	// 这里所有批次都成功，截断后可见集合必须是它们的前缀子集且无半截）。
	full := walSize(t, dir)
	for _, cut := range []int64{0, full / 5, full * 2 / 5, full * 3 / 5, full - 1, full} {
		p := filepath.Join(dir, walFileName)
		if err := os.Truncate(p, cut); err != nil {
			t.Fatal(err)
		}
		r := reopen(t, dir)
		for g := 0; g < goroutines; g++ {
			for i := 0; i < perG; i++ {
				prefix := fmt.Sprintf("g%d-k%d-", g, i)
				a, oka := r.Get(prefix + "a")
				b, okb := r.Get(prefix + "b")
				c, okc := r.Get(prefix + "c")
				if oka != okb || oka != okc {
					t.Fatalf("cut %d: batch g%d i%d split: %v %v %v", cut, g, i, oka, okb, okc)
				}
				if oka && (a != fmt.Sprintf("v%d-%d-a", g, i) || b != fmt.Sprintf("v%d-%d-b", g, i) || c != "") {
					t.Fatalf("cut %d: batch g%d i%d wrong values", cut, g, i)
				}
			}
		}
		r.Close()
	}
}
