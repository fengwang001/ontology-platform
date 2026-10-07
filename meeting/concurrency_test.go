package meeting

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentOpsSerializable 并发调用下结果等价于某个串行顺序：
// 用 -race 运行本测试验证无数据竞争；结束后通过快照检查核心不变量：
// 任一时刻发言者至多一人、发言者不在队列中、队列无重复成员、
// 队列长度不超过容量、恰有一名主持人。
func TestConcurrentOpsSerializable(t *testing.T) {
	r, err := NewRoom(3, 50)
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	nextNow := func() int64 { return clock.Add(1) }
	const workers = 8
	const opsPerWorker = 400
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			users := make([]string, 16)
			for i := range users {
				users[i] = fmt.Sprintf("w%d-u%d", seed, i)
			}
			pick := func() string { return users[rng.Intn(len(users))] }
			for i := 0; i < opsPerWorker; i++ {
				now := nextNow()
				switch rng.Intn(10) {
				case 0:
					_ = r.Join(pick(), now)
				case 1:
					_ = r.Leave(pick(), now)
				case 2, 3:
					_ = r.Raise(pick(), now)
				case 4:
					_ = r.Lower(pick(), now)
				case 5:
					_ = r.Grant(pick(), now)
				case 6:
					_ = r.Yield(pick(), now)
				case 7:
					_ = r.Mute(pick(), pick(), now)
				case 8:
					_, _ = r.Snapshot(now)
				case 9:
					_, _ = r.QueuePos(pick())
				}
			}
		}(int64(w))
	}
	wg.Wait()
	snap, err := r.Snapshot(nextNow())
	if err != nil {
		t.Fatalf("final snapshot: %v", err)
	}
	seen := make(map[string]bool)
	for _, u := range snap.Queue {
		if u == snap.Speaker && snap.Speaker != "" {
			t.Fatalf("speaker %q also in queue", u)
		}
		if seen[u] {
			t.Fatalf("duplicate %q in queue", u)
		}
		seen[u] = true
	}
	if len(snap.Queue) > 50 {
		t.Fatalf("queue len %d exceeds capacity", len(snap.Queue))
	}
	hosts := 0
	inRoom := make(map[string]MemberInfo)
	for _, m := range snap.Members {
		if m.Role == RoleHost {
			hosts++
		}
		inRoom[m.User] = m
	}
	if hosts != 1 {
		t.Fatalf("hosts = %d; want exactly 1", hosts)
	}
	for _, u := range snap.Queue {
		m, ok := inRoom[u]
		if !ok {
			t.Fatalf("queued user %q not in room", u)
		}
		if m.Muted {
			t.Fatalf("muted user %q in queue", u)
		}
	}
	t.Logf("final: members=%d queue=%d speaker=%q", len(snap.Members), len(snap.Queue), snap.Speaker)
}

// TestConcurrentGrantSingleSpeaker 并发 Grant 下恰好只有一个成功：
// 同一时刻发言者至多一人。
func TestConcurrentGrantSingleSpeaker(t *testing.T) {
	r, _ := NewRoom(3600, 500)
	mustOK(t, r.Join("host", 0))
	for i := 0; i < 100; i++ {
		mustOK(t, r.Join(fmt.Sprintf("u%d", i), 0))
		mustOK(t, r.Raise(fmt.Sprintf("u%d", i), 0))
	}
	var granted atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Grant("host", 1); err == nil {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != 1 {
		t.Fatalf("concurrent grants succeeded %d times; want exactly 1", granted.Load())
	}
	snap := mustSnapshot(t, r, 1)
	if snap.Speaker == "" {
		t.Fatal("expected exactly one speaker after concurrent grants")
	}
}
