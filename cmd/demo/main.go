// demo 逐条演示租约管理器的样例表与关键推导行为。
// 全部判定通过时退出码为 0，否则为 1。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/internal/lease"
)

var failed bool

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	var now int64
	m := lease.New(func() int64 { return now })

	tokA, err := m.Acquire("A", 100)
	check("空闲 Acquire(A) 成功且 token=1", err == nil && tokA == 1)

	_, err = m.Acquire("B", 100)
	check("A 持有中 Acquire(B) 失败(ErrLeaseHeld)", errors.Is(err, lease.ErrLeaseHeld))

	check("A 持有中 Write(tokenA) 成功", m.Write(tokA, "k", "v") == nil)
	got, ok := m.Read("k")
	check("Read(k) == v", ok && got == "v")

	now = 100 // 恰好到期：半开区间，已失效
	check("到期那一刻 Write 拒绝(ErrLeaseExpired)",
		errors.Is(m.Write(tokA, "k", "x"), lease.ErrLeaseExpired))
	check("到期那一刻 Renew 拒绝(ErrLeaseExpired)",
		errors.Is(m.Renew("A", tokA, 100), lease.ErrLeaseExpired))

	tokB, err := m.Acquire("B", 100)
	check("A 到期后 Acquire(B) 成功且 token=2", err == nil && tokB == 2)

	check("被抢占者旧 token Write 拒绝(ErrStaleToken)",
		errors.Is(m.Write(tokA, "k", "evil"), lease.ErrStaleToken))
	got, _ = m.Read("k")
	check("被拒绝的写无副作用，Read 仍为 v", got == "v")

	check("被抢占者 Renew 报 ErrNotHolder",
		errors.Is(m.Renew("A", tokA, 100), lease.ErrNotHolder))
	check("持有者重复 Acquire 报 ErrAlreadyHeld",
		func() bool { _, e := m.Acquire("B", 100); return errors.Is(e, lease.ErrAlreadyHeld) }())
	check("伪造巨大 token Write 报 ErrUnknownToken",
		errors.Is(m.Write(1<<60, "k", "forged"), lease.ErrUnknownToken))

	check("Release(B) 成功", m.Release("B", tokB) == nil)
	tokC, err := m.Acquire("C", 100)
	check("Release 后 Acquire(C) 成功且 token=3", err == nil && tokC == 3)
	check("重复 Release 报 ErrNotHolder",
		errors.Is(m.Release("B", tokB), lease.ErrNotHolder))

	_, err = m.Acquire("D", 0)
	check("ttl<=0 报 ErrInvalidTTL", errors.Is(err, lease.ErrInvalidTTL))
	_, err = m.Acquire("", 100)
	check("空 holder 报 ErrEmptyHolder", errors.Is(err, lease.ErrEmptyHolder))

	if failed {
		os.Exit(1)
	}
}
