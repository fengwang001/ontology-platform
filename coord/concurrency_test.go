package coord

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// TestConcurrentDriveProducesSingleDecision 并发投票、并发驱动、并发查询：
// 决议只产生一次，所有 goroutine 观察到的决议完全一致。
func TestConcurrentDriveProducesSingleDecision(t *testing.T) {
	now := base
	const n = 16
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("p%d", i))
	}
	c, parts := newWith(t, &now, ids...)

	var wg sync.WaitGroup
	outcomes := make(chan vote.Outcome, 3*n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("p%d", i)
			_ = c.Vote(id, true)
			outcomes <- c.Drive()
			outcomes <- c.Drive()
			_ = c.Query()
			outcomes <- c.Decide()
		}(i)
	}
	wg.Wait()
	close(outcomes)

	seen := make(map[vote.Outcome]int)
	for o := range outcomes {
		if o.Decision == vote.Undecided {
			continue // 未裁决的零值不算决议
		}
		seen[o]++
	}
	if len(seen) != 1 {
		t.Fatalf("决议不唯一：%v", seen)
	}
	for o := range seen {
		if o != (vote.Outcome{Decision: vote.Commit}) {
			t.Fatalf("got %+v, want Commit/None/empty", o)
		}
	}
	if got := c.Drive(); got != (vote.Outcome{Decision: vote.Commit}) {
		t.Fatalf("最终决议 %+v, want Commit", got)
	}
	for id, p := range parts {
		if p.State() != participant.Committed || p.CommitCount() != 1 {
			t.Fatalf("%s: state=%v count=%d, want Committed/1",
				id, p.State(), p.CommitCount())
		}
	}
}

// TestConcurrentTimeoutAndVotes 超时与投票并发竞争：决议唯一且写定后不变。
func TestConcurrentTimeoutAndVotes(t *testing.T) {
	now := base
	c, _ := newWith(t, &now, "a", "b")
	var wg sync.WaitGroup
	outcomes := make(chan vote.Outcome, 4)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Vote("a", true)
			outcomes <- c.Drive()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		now = base.Add(time.Minute)
		outcomes <- c.Drive()
	}()
	wg.Wait()
	close(outcomes)

	var first vote.Outcome
	for o := range outcomes {
		if o.Decision == vote.Undecided {
			continue
		}
		if first.Decision == vote.Undecided {
			first = o
			continue
		}
		if o != first {
			t.Fatalf("决议被改写：first %+v, then %+v", first, o)
		}
	}
	if got := c.Drive(); got != first {
		t.Fatalf("最终决议 %+v 与首次 %+v 不一致", got, first)
	}
}
