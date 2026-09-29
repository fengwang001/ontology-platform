package termination

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestConcurrentRace 并发乱序压力：投递、转空闲、传令牌、发送分别由
// 独立 goroutine 驱动；发送 worker 在注入完预置消息后自行退出，其余
// worker 持续推进直到 Done 关闭。配合 -race 验证无数据竞争。
func TestConcurrentRace(t *testing.T) {
	const n = 6
	d := mustNew(t, n)

	const total = 600

	// 预置一半消息，另一半由发送 worker 与其余动作并发注入。
	for i := 0; i < total/2; i++ {
		_, err := d.Send(i%n, (i*3+1)%n)
		if err != nil {
			t.Fatal(err)
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	stopping := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}

	// 发送 worker：只能在还有活跃进程时成功；发完全部预算后退出。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := total / 2; i < total; i++ {
			if stopping() {
				return
			}
			snap := d.Snapshot()
			var actives []int
			for p, st := range snap.States {
				if st == Active {
					actives = append(actives, p)
				}
			}
			if len(actives) == 0 {
				return // 全员空闲后无法再发送
			}
			from := actives[i%len(actives)]
			to := (from + 1 + i%(n-1)) % n
			_, err := d.Send(from, to)
			if err != nil {
				if errors.Is(err, ErrIdleSender) || errors.Is(err, ErrTerminated) {
					return
				}
				t.Errorf("Send: %v", err)
				return
			}
		}
	}()

	// 投递 worker：不断取出最早的在途消息投递。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stopping() {
			pending := d.net.Pending()
			if len(pending) == 0 {
				continue
			}
			if _, err := d.Deliver(pending[0].ID); err != nil &&
				!errors.Is(err, ErrMessageNotFound) && !errors.Is(err, ErrTerminated) {
				t.Errorf("Deliver: %v", err)
				return
			}
		}
	}()

	// 转空闲 worker：循环把任意活跃进程转空闲（投递可能再次激活它）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stopping() {
			snap := d.Snapshot()
			for p, st := range snap.States {
				if st == Active {
					if err := d.BecomeIdle(p); err != nil &&
						!errors.Is(err, ErrAlreadyIdle) && !errors.Is(err, ErrTerminated) {
						t.Errorf("BecomeIdle: %v", err)
						return
					}
					break
				}
			}
		}
	}()

	// 令牌 worker：持有者空闲就传，且持续检查不变量。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stopping() {
			snap := d.Snapshot()
			if sum(snap.Counts) != snap.Pending {
				t.Errorf("invariant: sum(counts)=%d pending=%d", sum(snap.Counts), snap.Pending)
				return
			}
			if snap.States[snap.Holder] != Idle {
				continue
			}
			ann, _, err := d.PassToken(snap.Holder)
			if err != nil {
				if errors.Is(err, ErrActiveHolder) || errors.Is(err, ErrNoToken) ||
					errors.Is(err, ErrTerminated) {
					continue
				}
				t.Errorf("PassToken: %v", err)
				return
			}
			if ann {
				return
			}
		}
	}()

	select {
	case <-d.Done():
	case <-time.After(10 * time.Second):
		close(stop)
		wg.Wait()
		t.Fatal("termination not announced under concurrent driving")
	}
	close(stop)
	wg.Wait()

	r, ok := d.Announced()
	if !ok {
		t.Fatal("not announced")
	}
	assertQuiescent(t, d, n)
	t.Logf("concurrent run terminated at round %d", r)
}
