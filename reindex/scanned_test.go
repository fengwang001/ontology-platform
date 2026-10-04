package reindex_test

import (
	"fmt"
	"strconv"
	"sync"
	"testing"

	"ontology/reindex"
)

// TestScannedProof：快照 1000 与 100000 两档对照，
// 单次 Step 读取条目数恰为 min(B, 剩余)，与快照总量无关。
func TestScannedProof(t *testing.T) {
	for _, total := range []int{1000, 100000} {
		for _, B := range []int{1, 7, 1000} {
			c, err := reindex.New(65536)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < total; i++ {
				id := strconv.Itoa(i)
				if _, e := c.Put(id, "v"); e != nil {
					t.Fatalf("put %s: %v", id, e)
				}
			}
			if err := c.Start(B); err != nil {
				t.Fatalf("total=%d B=%d start: %v", total, B, err)
			}
			prev := 0
			steps := 0
			for {
				ap, cf, ic, done, e := c.Step()
				if e != nil {
					t.Fatal(e)
				}
				cur := c.Scanned()
				read := cur - prev
				remain := total - prev
				want := B
				if remain < want {
					want = remain
				}
				if read != want {
					t.Fatalf("total=%d B=%d step#%d read=%d want=%d", total, B, steps, read, want)
				}
				if ap+cf+ic != read {
					t.Fatalf("total=%d B=%d step#%d sum(%d+%d+%d)=%d != read %d",
						total, B, steps, ap, cf, ic, ap+cf+ic, read)
				}
				prev = cur
				steps++
				if done {
					break
				}
			}
			if prev != total {
				t.Fatalf("total=%d B=%d scanned=%d", total, B, prev)
			}
			wantSteps := (total + B - 1) / B
			if steps != wantSteps {
				t.Fatalf("total=%d B=%d steps=%d want %d", total, B, steps, wantSteps)
			}
		}
	}
}

// TestConcurrencyRace：并发源写入 / 回填 / 判定，配合 -race 验证线性化无数据竞争。
func TestConcurrencyRace(t *testing.T) {
	c, err := reindex.New(4)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 8
	for i := 0; i < 64; i++ {
		_, _ = c.Put(fmt.Sprintf("id%d", i), "x")
	}
	if err := c.Start(3); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("id%d", (base+i)%128)
				if i%4 == 0 {
					_, _ = c.Delete(id)
				} else {
					_, _ = c.Put(id, []string{"x", "yy", "toolong"}[i%3])
				}
			}
		}(w * 31)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if _, _, _, done, _ := c.Step(); done {
				return
			}
		}
	}()
	wg.Wait()
	for {
		if _, _, _, done, _ := c.Step(); done {
			break
		}
	}
	if err := c.Cutover(1_000_000); err != nil {
		t.Fatalf("cutover: %v", err)
	}
}
