package coord

import (
	"sync"
	"testing"
)

// 复杂度：Decide/Recover 检查过的参与者个数不随 m 增长（白盒读非导出计数器）。
func TestDecideChecksConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		c := New(m)
		for p := 0; p < m; p++ {
			if err := c.Vote(p, true); err != nil {
				t.Fatal(err)
			}
		}
		if d, _ := c.Decide(); d != Commit {
			t.Fatalf("m=%d decide=%v", m, d)
		}
		if c.checked > 1 {
			t.Fatalf("m=%d: Decide checked %d participants, want O(1)", m, c.checked)
		}
		if c.Recover() != Commit || c.checked > 1 {
			t.Fatalf("m=%d: Recover checked %d", m, c.checked)
		}
	}
}

// 并发：M 个 goroutine 对不同参与者投 yes，已投票计数单调不减，最终 Commit。
func TestConcurrentVotesDecideCommit(t *testing.T) {
	const m = 64
	c := New(m)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 2; g++ { // 投票期间并发调用 Decide（未投齐只报错，不改状态）
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					c.Decide()
				}
			}
		}()
	}
	voted := make(chan struct{}, m)
	for p := 0; p < m; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			if err := c.Vote(p, true); err != nil {
				t.Errorf("vote %d: %v", p, err)
			}
			voted <- struct{}{}
		}(p)
	}
	prev := 0
	for i := 0; i < m; i++ {
		<-voted
		if y, _ := c.Counts(); y < prev {
			t.Errorf("voted count decreased: %d -> %d", prev, y)
		} else {
			prev = y
		}
	}
	close(done)
	wg.Wait()
	// 投齐后并发 Decide/Recover，必须都得到 Commit
	var wg2 sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg2.Add(1)
		go func(g int) {
			defer wg2.Done()
			if d, err := c.Decide(); err != nil || d != Commit {
				t.Errorf("g%d Decide=%v,%v", g, d, err)
			}
			if d := c.Recover(); d != Commit {
				t.Errorf("g%d Recover=%v", g, d)
			}
		}(g)
	}
	wg2.Wait()
}
