package billing

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestConcurrentSerializable 并发提交/撤回/查询/概览/结算：
// 1) -race 下无数据竞争；
// 2) 终态与某条串行顺序的朴素模型等价（以最终值多重集与结算结果校验）；
// 3) 每步输入、输出与判定依据写日志。
func TestConcurrentSerializable(t *testing.T) {
	const writers = 8
	const slotsPerWriter = 25
	const rounds = 5
	const n = writers * slotsPerWriter

	logPath := filepath.Join(os.TempDir(), "billing-concurrent.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	mw := io.MultiWriter(os.Stdout, logFile)

	b, _ := NewBiller(0, n)

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			base := id * slotsPerWriter
			for round := 0; round < rounds; round++ {
				for j := 0; j < slotsPerWriter; j++ {
					slot := base + j
					// 每槽位一轮内固定四步：v1 提交、v2 覆盖、
					// v2 撤回（回到缺失）、v3 重新提交。
					v1 := int64(round*4 + 1)
					v2 := int64(round*4 + 2)
					v3 := int64(round*4 + 3)
					steps := []struct {
						name string
						op   func() error
					}{
						{"submit", func() error {
							return b.Submit(Sample{slot, v1, v1 + 1, v1})
						}},
						{"submit", func() error {
							return b.Submit(Sample{slot, v2, v2 + 1, v2})
						}},
						{"withdraw", func() error { return b.Withdraw(slot, v2) }},
						{"submit", func() error {
							return b.Submit(Sample{slot, v3, v3 + 1, v3})
						}},
					}
					for _, st := range steps {
						e := st.op()
						fmt.Fprintf(mw, "goroutine=%d %s{slot=%d} -> %v\n", id, st.name, slot, e)
					}
				}
			}
		}(w)
	}

	// 读者只做只读操作，随时应返回内部自洽的快照。
	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	for r := 0; r < 4; r++ {
		readerWg.Add(1)
		go func(id int) {
			defer readerWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				o := b.Overview()
				if o.ReceivedSlots+o.MissingSlots != n {
					t.Errorf("reader %d bad counts: %+v", id, o)
					return
				}
				if o.RateDefined {
					// 无采样以外的任何快照都必须给出 [0, MaxRate] 内速率。
					if o.Rate < 0 || o.Rate > MaxRate {
						t.Errorf("reader %d bad rate: %+v", id, o)
						return
					}
				}
				rate, qerr := b.CurrentRate()
				if qerr != nil && errCode(qerr) != ErrNoSamples {
					t.Errorf("reader %d unexpected query error: %v", id, qerr)
					return
				}
				if qerr == nil && (rate < 0 || rate > MaxRate) {
					t.Errorf("reader %d bad queried rate: %d", id, rate)
					return
				}
			}
		}(r)
	}

	wg.Wait()
	close(stop)
	readerWg.Wait()

	// 终态校验：槽位在 goroutine 间互不相交，每槽位操作由唯一 goroutine
	// 按固定顺序发出，因此任何串行化终态相同，可用朴素模型重放校验。
	naive := NewNaiveBiller(0, n)
	for w := 0; w < writers; w++ {
		base := w * slotsPerWriter
		for round := 0; round < rounds; round++ {
			for j := 0; j < slotsPerWriter; j++ {
				slot := base + j
				v1 := int64(round*4 + 1)
				v2 := int64(round*4 + 2)
				v3 := int64(round*4 + 3)
				_ = naive.Submit(Sample{slot, v1, v1 + 1, v1})
				_ = naive.Submit(Sample{slot, v2, v2 + 1, v2})
				_ = naive.Withdraw(slot, v2)
				_ = naive.Submit(Sample{slot, v3, v3 + 1, v3})
			}
		}
	}
	no := naive.Overview()
	ro := b.Overview()
	if no != ro {
		t.Fatalf("final overview diverged from serial naive model:\n real=%+v\nnaive=%+v", ro, no)
	}

	// 结算：并发期结束后封存，再用不同参数重复结算必须得到同一缓存结果。
	first, err := b.Settle(400, 10000)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	nfirst, nerr := naive.Settle(400, 10000)
	if nerr != nil || *first != *nfirst {
		t.Fatalf("settlement diverged: real=%+v err=%v naive=%+v err=%v", first, err, nfirst, nerr)
	}
	second, _ := b.Settle(1, 0)
	if !second.Repeated || second.BilledRate != first.BilledRate || second.Base != first.Base {
		t.Fatalf("repeat after concurrent settle: %+v vs %+v", second, first)
	}
	t.Logf("concurrent OK: received=%d missing=%d billed=%d; log=%s",
		no.ReceivedSlots, no.MissingSlots, first.BilledRate, logPath)
}
