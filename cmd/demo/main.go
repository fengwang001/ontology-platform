// demo 逐条验证崩溃可恢复分片账本的核心语义，全部通过时以退出码 0 结束。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/ledger"
	"ontology/shard"
	"ontology/wal"
)

type sink struct{ fail bool }

func (s *sink) Write(p []byte) (int, error) {
	if s.fail {
		return 0, errors.New("disk on fire")
	}
	return len(p), nil
}

var failed bool

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failed = true
	}
	fmt.Printf("%-4s %s\n", verdict, name)
}

func newLedger(n int, initial int64) (*ledger.Ledger, *wal.Log, *shard.Set, *sink) {
	sk := &sink{}
	l := wal.New(sk)
	s := shard.New(n, initial)
	return ledger.New(l, s), l, s, sk
}

func main() {
	// 1. 先日志后应用：落盘失败时状态不变、不占 Seq。
	g, l, s, sk := newLedger(2, 100)
	sk.fail = true
	err := g.Transfer(0, 1, 10)
	check("1 先日志后应用: 落盘失败状态不变且不占Seq",
		err != nil && s.Total() == 200 && s.Balance(0) == 100 && l.LastSeq() == 0)
	// 2. 总额守恒。
	sk.fail = false
	_ = g.Transfer(0, 1, 30)
	_ = g.Transfer(1, 0, 5)
	check("2 总额守恒: Total == 分片数*初始余额", s.Total() == 200)
	// 3. 幂等重放：第二次 Recover 的 replayed 为 0。
	g2, _, s2, _ := newLedger(2, 100)
	g2.CrashAt(ledger.CrashAfterDebit)
	_ = g2.Transfer(0, 1, 40)
	n1, _ := g2.Recover()
	n2, _ := g2.Recover()
	check("3 幂等重放: 二次Recover replayed==0 且状态一致",
		n1 > 0 && n2 == 0 && s2.Balance(0) == 60 && s2.Balance(1) == 140)
	// 4. 三个崩溃点都能恢复到与不崩溃一致。
	ok := true
	for _, p := range []ledger.CrashPoint{ledger.CrashAfterAppend, ledger.CrashAfterDebit, ledger.CrashAfterApply} {
		gb, _, sb, _ := newLedger(3, 100)
		gc, _, sc, _ := newLedger(3, 100)
		_ = gb.Transfer(0, 1, 10)
		_ = gc.Transfer(0, 1, 10)
		_ = gb.Transfer(1, 2, 25)
		gc.CrashAt(p)
		crashErr := gc.Transfer(1, 2, 25)
		_, recErr := gc.Recover()
		for i := 0; i < 3; i++ {
			ok = ok && sb.Balance(i) == sc.Balance(i)
		}
		ok = ok && errors.Is(crashErr, ledger.ErrCrashed) && recErr == nil && sc.Total() == 300
	}
	check("4 三崩溃点: 中断返回错误且Recover后逐片一致", ok)
	// 5. 检查点：只截断已全部应用的记录，截断后恢复仍守恒。
	g3, l3, s3, _ := newLedger(2, 100)
	_ = g3.Transfer(0, 1, 10)
	g3.CrashAt(ledger.CrashAfterAppend)
	_ = g3.Transfer(0, 1, 20)
	_ = g3.Checkpoint()
	left, _ := l3.Replay(0)
	_, recErr := g3.Recover()
	check("5 检查点: 未应用记录不被截断, 截断后恢复守恒",
		len(left) == 2 && recErr == nil && s3.Balance(0) == 70 && s3.Total() == 200)
	// 6. 参数与余额校验：全部拒绝且不留 WAL 记录。
	g4, l4, s4, _ := newLedger(2, 100)
	rejected := g4.Transfer(0, 1, 0) != nil &&
		g4.Transfer(0, 1, -1) != nil &&
		g4.Transfer(1, 1, 5) != nil &&
		g4.Transfer(0, 9, 5) != nil &&
		g4.Transfer(0, 1, 101) != nil
	check("6 校验: 非法参数/余额不足拒绝且WAL无残留",
		rejected && l4.LastSeq() == 0 && s4.Total() == 200)
	// 7. 并发转账：总额守恒、Seq 连续、同 Txn 记录相邻。
	g5, l5, s5, _ := newLedger(6, 100000)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				_ = g5.Transfer((w+i)%6, (w+i+1)%6, 1)
			}
		}(w)
	}
	wg.Wait()
	recs, _ := l5.Replay(0)
	ok = len(recs)%2 == 0 && s5.Total() == 600000
	for i, r := range recs {
		ok = ok && r.Seq == uint64(i+1) && (i%2 == 0 || recs[i-1].Txn == r.Txn)
	}
	check("7 并发转账: 总额守恒 Seq连续 同Txn相邻", ok)
	// 8. 恢复与并发交叉：Recover 独占串行化，不变量保持。
	g6, _, s6, _ := newLedger(4, 100000)
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					_ = g6.Transfer((w+i)%4, (w+i+1)%4, 1)
				}
			}
		}(w)
	}
	for i := 0; i < 50; i++ {
		_, _ = g6.Recover()
		_ = g6.Checkpoint()
	}
	close(stop)
	wg.Wait()
	_, err = g6.Recover()
	check("8 恢复与并发交叉: Recover独占 总额守恒", err == nil && s6.Total() == 400000)
	if failed {
		os.Exit(1)
	}
}
