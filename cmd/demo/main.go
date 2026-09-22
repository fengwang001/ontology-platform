// demo 逐条演示 internal/lease 样例表中的场景，打印 OK/FAIL。
// 有任何一项 FAIL 时退出码为 1。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/internal/lease"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%-4s %s\n", status, name)
}

func main() {
	var now int64
	m := lease.New(func() int64 { return now })

	tokA, err := m.Acquire("A", 100)
	check("空闲时 Acquire(A) 成功且 token=1", err == nil && tokA == 1)

	_, err = m.Acquire("B", 100)
	check("A 持有中 Acquire(B) 失败(ErrLeaseHeld)", errors.Is(err, lease.ErrLeaseHeld))

	check("A 持有中 Write(tokenA) 成功", m.Write(tokA, "k", "v") == nil)
	val, ok := m.Read("k")
	check("Read(k)=v", ok && val == "v")

	now = 100 // 到期边界：now == expiresAt
	check("到期那一刻 Write 被拒(ErrLeaseExpired)",
		errors.Is(m.Write(tokA, "k", "x"), lease.ErrLeaseExpired))
	check("到期那一刻 Renew 被拒(ErrLeaseExpired)",
		errors.Is(m.Renew("A", tokA, 100), lease.ErrLeaseExpired))

	tokB, err := m.Acquire("B", 100)
	check("到期后 Acquire(B) 成功且 token=2", err == nil && tokB == 2)

	check("被抢占者旧 token Write 被拒(ErrStaleToken)",
		errors.Is(m.Write(tokA, "k", "evil"), lease.ErrStaleToken))
	val, _ = m.Read("k")
	check("被拒的写无副作用 Read(k)=v", val == "v")
	check("被抢占者 Renew 被拒(ErrNotHolder)",
		errors.Is(m.Renew("A", tokA, 100), lease.ErrNotHolder))
	check("被抢占者 Release 被拒(ErrNotHolder)",
		errors.Is(m.Release("A", tokA), lease.ErrNotHolder))

	check("伪造巨大 token Write 同属 ErrStaleToken",
		errors.Is(m.Write(^uint64(0), "k", "x"), lease.ErrStaleToken))

	check("B Release 成功", m.Release("B", tokB) == nil)
	tokC, err := m.Acquire("C", 100)
	check("Release 后 Acquire(C) 成功且 token 递增", err == nil && tokC == 3)

	_, err = m.Acquire("D", 0)
	check("ttl<=0 被拒(ErrInvalidTTL)", errors.Is(err, lease.ErrInvalidTTL))
	_, err = m.Acquire("", 100)
	check("空 holder 被拒(ErrInvalidHolder)", errors.Is(err, lease.ErrInvalidHolder))

	tokC2, err := m.Acquire("C", 100)
	check("同人重复 Acquire 发新 token", err == nil && tokC2 == 4)
	check("旧 token 立即失效",
		errors.Is(m.Write(tokC, "k", "x"), lease.ErrStaleToken))

	now = 1000 // 让 C 过期，但记录仍在
	check("过期后 Release 幂等成功", m.Release("C", tokC2) == nil)

	if failed {
		os.Exit(1)
	}
}

// 以下为追加部分：用独立的管理器演练三条最关键边界（不改动上面现有内容）。
// 输出固定 4 行（1 行小节标题 + 3 条 OK/FAIL），在 main 之前执行。
func init() {
	var now int64
	m := lease.New(func() int64 { return now })

	fmt.Println("== 三条关键边界 ==")
	tok, _ := m.Acquire("X", 10)
	now = 10 // 边界 1：到期那一刻续约与写入都失效
	check("边界1 到期那一刻 Renew/Write 均失效",
		errors.Is(m.Renew("X", tok, 10), lease.ErrLeaseExpired) &&
			errors.Is(m.Write(tok, "k", "z"), lease.ErrLeaseExpired))

	tokY, _ := m.Acquire("Y", 100) // 边界 2：旧持有者被接管
	check("边界2 被抢占后旧 token 写被拒且 Read 不变",
		errors.Is(m.Write(tok, "k", "evil"), lease.ErrStaleToken) &&
			func() bool { v, ok := m.Read("k"); return !ok && v == "" }())

	now = 200
	tokY2, _ := m.Acquire("Y", 100) // 边界 3：同 holder 有效重获
	check("边界3 同 holder 重获发新 token 且旧号失效",
		tokY2 == tokY+1 && errors.Is(m.Write(tokY, "k", "old"), lease.ErrStaleToken))
}
