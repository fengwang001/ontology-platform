package coord

import (
	"sync"
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// TestConcurrentDecisionHappensOnce 并发投票、并发驱动、并发查询：
// 决议必须只产生一次且所有 goroutine 观察到同一结果。
func TestConcurrentDecisionHappensOnce(t *testing.T) {
	const voters = 16
	clock := newFakeClock()
	c := New(clock.now)
	ids := make([]string, voters)
	for i := range ids {
		ids[i] = string(rune('a' + i))
		if err := c.Register(ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Begin(time.Minute); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	verdicts := make(chan vote.Verdict, voters*2)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = c.CastVote(id, vote.Agree)
		}(id)
	}
	for i := 0; i < voters; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if v, err := c.Drive(); err == nil && v.Decision != vote.None {
				verdicts <- v
			}
		}()
		go func() {
			defer wg.Done()
			_ = c.Query()
		}()
	}
	wg.Wait()
	close(verdicts)

	final, err := c.Drive()
	if err != nil || final.Decision != vote.Commit {
		t.Fatalf("final drive must be COMMIT, got %v err=%v", final, err)
	}
	for v := range verdicts {
		if v != final {
			t.Fatalf("concurrent drives observed different verdicts: %v vs %v", v, final)
		}
	}
	st := c.Query()
	for _, id := range ids {
		if st.Participants[id] != participant.Committed {
			t.Fatalf("%s must be Committed, got %v", id, st.Participants[id])
		}
		if st.CommitCounts[id] != 1 {
			t.Fatalf("%s commit must run exactly once, got %d", id, st.CommitCounts[id])
		}
	}
}

// TestConcurrentReplayAndCommands 并发重放与重复指令保持幂等。
func TestConcurrentReplayAndCommands(t *testing.T) {
	c, _ := newStarted(t, "a", "b")
	_ = c.CastVote("a", vote.Agree)
	_ = c.CastVote("b", vote.Agree)
	if _, err := c.Drive(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); _ = c.Replay() }()
		go func() { defer wg.Done(); _ = c.SendCommand("a", vote.Commit) }()
		go func() { defer wg.Done(); _ = c.Query() }()
	}
	wg.Wait()
	st := c.Query()
	if st.CommitCounts["a"] != 1 || st.CommitCounts["b"] != 1 {
		t.Fatalf("commit counts must stay 1 under concurrency, got %v", st.CommitCounts)
	}
}
