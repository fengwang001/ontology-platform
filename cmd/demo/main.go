// 演示带围栏令牌的租约互斥服务：互斥、续期、过期边界、
// 令牌单调、旧令牌拒绝、水位不回退、释放、不可复活、零值查询、并发唯一。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/fence"
	"ontology/lockd"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

var checks, failures int

func check(name string, ok bool, detail string) {
	checks++
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %-22s %s\n", status, name, detail)
}

func main() {
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	svc := lockd.New(c.Now)
	const ttl = 10 * time.Second

	// 1. 互斥：alice 持有时 bob 抢不到，错误带持有者与剩余时长。
	tokA, _ := svc.Acquire("db", "alice", ttl)
	_, err := svc.Acquire("db", "bob", ttl)
	check("互斥", errors.Is(err, lockd.ErrHeld), fmt.Sprintf("bob 被拒: %v", err))

	// 2. 续期只能由持有者发起，且不换令牌。
	c.Advance(4 * time.Second)
	_, errBob := svc.Renew("db", "bob", ttl)
	tokA2, errRenew := svc.Renew("db", "alice", ttl)
	info := svc.Info("db")
	check("续期鉴权与令牌", errors.Is(errBob, lockd.ErrNotHolder) && errRenew == nil && tokA2 == tokA && info.Remaining == ttl,
		fmt.Sprintf("bob 续期被拒, alice 令牌不变=%d, 剩余重置为 %s", tokA2, info.Remaining))

	// 3. now 恰好等于到期时间即过期，他人可抢。
	c.Advance(ttl)
	expired := !svc.Info("db").Held
	tokB, errB := svc.Acquire("db", "bob", ttl)
	check("到期时刻判过期", expired && errB == nil, fmt.Sprintf("now==expiry 时 bob 抢到, 令牌=%d", tokB))

	// 4. 令牌跨轮严格递增。
	check("令牌严格递增", tokB > tokA, fmt.Sprintf("alice=%d -> bob=%d", tokA, tokB))

	// 5. 旧持有者拿过期令牌写入被拒，且与「无人持有」可区分。
	errStale := svc.Write("db", tokA, []byte("stale"))
	errNoHolder := svc.Write("other", 1, []byte("x"))
	check("旧令牌写入被拒", errors.Is(errStale, lockd.ErrStaleToken) && errors.Is(errNoHolder, lockd.ErrNotHeld) && !errors.Is(errStale, lockd.ErrNotHeld),
		fmt.Sprintf("stale=%v; 无人持有=%v", errStale, errNoHolder))

	// 6. 水位不回退：更大令牌被接受后，等于旧水位的令牌也被拒。
	_ = svc.Write("db", tokB+5, []byte("high"))
	errLow := svc.Write("db", tokB, []byte("old-level"))
	check("水位不回退", errors.Is(errLow, lockd.ErrStaleToken), fmt.Sprintf("水位抬到 %d 后令牌 %d 被拒", tokB+5, tokB))

	// 7. 主动释放后立刻可抢。
	_ = svc.Release("db", "bob")
	tokC, errC := svc.Acquire("db", "carol", ttl)
	check("释放后立刻可抢", errC == nil && tokC > tokB, fmt.Sprintf("carol 拿到令牌=%d", tokC))

	// 8. 过期后旧持有者无法复活。
	c.Advance(ttl)
	_, errRevive := svc.Renew("db", "carol", ttl)
	check("过期无法复活", errors.Is(errRevive, lockd.ErrNotHolder), fmt.Sprintf("carol 续期被拒: %v", errRevive))

	// 9. 无人持有时查询为零值。
	info = svc.Info("db")
	check("无人持有查询零值", !info.Held && info.Holder == "" && info.Remaining == 0 && info.Token == 0,
		fmt.Sprintf("info=%+v", info))

	// 10. 并发下恰好一个 Acquire 成功。
	const n = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	tokens := map[fence.Token]bool{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := svc.Acquire("race", fmt.Sprintf("c%d", i), time.Minute)
			if err == nil {
				mu.Lock()
				wins++
				tokens[tok] = true
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	check("并发唯一成功者", wins == 1 && len(tokens) == 1, fmt.Sprintf("%d 个 goroutine 中 %d 个成功", n, wins))

	fmt.Printf("----\n%d/%d 项通过\n", checks-failures, checks)
	if failures > 0 {
		os.Exit(1)
	}
}
