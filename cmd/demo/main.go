// 演示带围栏令牌的租约互斥服务：逐条演练核心语义并打印判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology/fence"
	"ontology/internal/testclock"
	"ontology/lease"
	"ontology/lockd"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
	} else {
		failed++
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	clock := testclock.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	d := lockd.New(clock.Now)
	const ttl = 10 * time.Second

	tokA, _ := d.Acquire("res", "alice", ttl)
	_, errHeld := d.Acquire("res", "bob", ttl)
	var held *lease.HeldError
	check("互斥: 未过期时他人抢不到且错误带持有者",
		errors.As(errHeld, &held) && held.Holder == "alice" && held.Remaining == ttl)

	check("续期: 非持有者被拒", errors.Is(d.Renew("res", "bob", ttl), lease.ErrNotHolder))

	clock.Advance(4 * time.Second)
	_ = d.Renew("res", "alice", ttl)
	st := d.Status("res")
	check("续期: 推到 now+ttl 且令牌不变", st.Remaining == ttl && st.Token == tokA)

	clock.Advance(ttl) // now 恰好等于到期时间
	tokB, errB := d.Acquire("res", "bob", ttl)
	check("过期: now==到期时刻即失效且他人可抢", errB == nil && d.Status("res").Holder == "bob")

	check("令牌: 跨轮严格递增", tokB > tokA)

	check("围栏: 旧令牌写入被拒", errors.Is(d.Write("res", tokA, []byte("s")), fence.ErrStale))

	okWrite := d.Write("res", tokB, []byte("v")) == nil
	check("水位: 新令牌可写且旧水位令牌仍被拒",
		okWrite && errors.Is(d.Write("res", tokA, nil), fence.ErrStale))

	_ = d.Release("res", "bob")
	tokC, errC := d.Acquire("res", "carol", ttl)
	check("释放: 主动释放后立刻可抢且令牌更大", errC == nil && tokC > tokB)

	clock.Advance(ttl + time.Second)
	errRevive := d.Renew("res", "carol", ttl)
	tokD, errD := d.Acquire("res", "dave", ttl)
	check("过期: 无法复活须重新获取且令牌更新",
		errors.Is(errRevive, lease.ErrNotHeld) && errD == nil && tokD > tokC)

	_ = d.Release("res", "dave")
	st = d.Status("res")
	check("查询: 无人持有时持有者与剩余时长为零值",
		!st.Held && st.Holder == "" && st.Remaining == 0)

	errWrite := d.Write("res", tokD, nil)
	check("写入: 无人持有与令牌过旧可区分",
		errors.Is(errWrite, lockd.ErrNotHeld) && !errors.Is(errWrite, fence.ErrStale))

	const n = 16
	var wg sync.WaitGroup
	var wins atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := d.Acquire("race", fmt.Sprintf("c%d", i), ttl); err == nil {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	check("并发: 16 个竞争者中恰好一个抢到", wins.Load() == 1)

	fmt.Printf("TOTAL %d/%d passed\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
