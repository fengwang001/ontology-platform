package planningpoker

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentDeterminism 验证“并发调用结果等价于某个串行顺序”。
//
// 所有并发操作使用同一个合法时刻 now（相等时间戳不构成回退），因此
// 它们的任意交错都被时钟接受；互斥锁保证一次只有一个操作推进状态，
// 自动揭示至多发生一次。配合 -race 检测数据竞争。
func TestConcurrentDeterminism(t *testing.T) {
	const voters = 64
	s := testSession(t, true, 3, 86400, 0)
	for i := 0; i < voters; i++ {
		mustJoin(t, s, fmt.Sprintf("v%d", i), RoleVoter, int64(i+1))
	}
	mustStart(t, s, voters+1)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var reveals []*RevealResult
	const now = int64(voters + 2)

	for i := 0; i < voters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := fmt.Sprintf("v%d", i)
			// 多次改投/Peek 均在同一时刻，验证同刻并发下的串行化。
			var r *RevealResult
			r1, err := s.Vote(u, numCard(1), now)
			if err != nil {
				t.Errorf("vote1: %v", err)
				return
			}
			r2, _ := s.Vote(u, numCard(5), now)
			_, _ = s.Peek(u, now)
			r3, _ := s.Vote(u, numCard(3), now)
			for _, rr := range []*RevealResult{r1, r2, r3} {
				if rr != nil {
					r = rr
				}
			}
			if r != nil {
				mu.Lock()
				reveals = append(reveals, r)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if len(reveals) == 0 {
		t.Fatalf("auto reveal must occur once all vote")
	}
	cat := reveals[0].Category
	for _, r := range reveals[1:] {
		if r.Category != cat {
			t.Fatalf("concurrent reveals disagree: %d vs %d", r.Category, cat)
		}
	}

	v := mustPeek(t, s, "v0", now+1)
	if v.Phase == PhaseVoting {
		t.Fatalf("session must be revealed")
	}
	if v.Outcome == nil || v.Outcome.NumericVotes != voters {
		t.Fatalf("want %d numeric votes, got %+v", voters, v.Outcome)
	}
}

// TestConcurrentMixedOpsRace 仅用于在 -race 下长时间混合调用，
// 正确性等价性由差分测试与上面的确定性测试覆盖。
func TestConcurrentMixedOpsRace(t *testing.T) {
	s := testSession(t, false, 3, 86400, 0)
	const users = 16
	for i := 0; i < users; i++ {
		mustJoin(t, s, fmt.Sprintf("u%d", i), RoleVoter, int64(i+1))
	}
	mustStart(t, s, users+1)

	var wg sync.WaitGroup
	for round := 0; round < 300; round++ {
		now := int64(users + 2 + round)
		for i := 0; i < users; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				u := fmt.Sprintf("u%d", i)
				card := numCard([]int{1, 2, 3, 5, 8, 13}[(round+i)%6])
				_, _ = s.Vote(u, card, now)
				_, _ = s.Peek(u, now)
			}(i)
		}
	}
	wg.Wait()
}
