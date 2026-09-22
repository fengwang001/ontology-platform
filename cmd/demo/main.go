package main

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology/gateway"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func main() {
	clk := &clock{t: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	var joinWG sync.WaitGroup
	gw := gateway.New(gateway.Config{
		Now:    clk.now,
		TTL:    10 * time.Minute,
		OnJoin: func(string) { joinWG.Done() },
	})

	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}

	// 1. 恰好执行一次 + 2. 回放可区分
	var calls1 atomic.Int64
	exec1 := func([]byte) ([]byte, error) { calls1.Add(1); return []byte("created-1"), nil }
	first := gw.Submit("k1", []byte("body"), exec1)
	replay := gw.Submit("k1", []byte("body"), exec1)
	once := calls1.Load() == 1 && gw.ExecCalls() == 1
	equiv := !first.Replayed && replay.Replayed && bytes.Equal(first.Result, replay.Result)
	check("恰好执行一次（重复提交执行计数恒为一）", once)
	check("回放等价且可区分（Replayed=true，结果相同）", equiv)

	// 3. 异体冲突：不执行、不覆盖
	var calls3 atomic.Int64
	exec3 := func([]byte) ([]byte, error) { calls3.Add(1); return []byte("should-not-happen"), nil }
	conflict := gw.Submit("k1", []byte("different-body"), exec3)
	again := gw.Submit("k1", []byte("body"), exec3)
	check("异体立刻冲突且不执行不覆盖", gateway.IsConflict(conflict.Err) &&
		calls3.Load() == 0 && again.Replayed && bytes.Equal(again.Result, []byte("created-1")))

	// 4. 失败可重试，成功后固定
	var failOnce atomic.Bool
	exec4 := func([]byte) ([]byte, error) {
		if failOnce.CompareAndSwap(false, true) {
			return nil, fmt.Errorf("transient-error")
		}
		return []byte("final"), nil
	}
	f1 := gw.Submit("k4", []byte("b"), exec4)
	f2 := gw.Submit("k4", []byte("b"), exec4)
	f3 := gw.Submit("k4", []byte("b"), exec4)
	check("失败原样返回、可重试、成功后固定只回放", f1.Err != nil && f1.Err.Error() == "transient-error" &&
		!f2.Replayed && bytes.Equal(f2.Result, []byte("final")) && f3.Replayed && gw.ExecCalls() == 3)

	// 5/6. 执行中：同体合流，异体立刻冲突不阻塞
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	slowExec := func([]byte) ([]byte, error) {
		started <- struct{}{}
		<-release
		return []byte("joined"), nil
	}
	go gw.Submit("k6", []byte("same"), slowExec)
	<-started
	joinWG.Add(8)
	var wg sync.WaitGroup
	outs := make([]gateway.Outcome, 8)
	wg.Add(8)
	for i := 0; i < 8; i++ {
		go func(i int) { defer wg.Done(); outs[i] = gw.Submit("k6", []byte("same"), slowExec) }(i)
	}
	conflictCh := make(chan gateway.Outcome, 1)
	go func() { conflictCh <- gw.Submit("k6", []byte("other"), slowExec) }()
	joinWG.Wait() // 8 个同体后来者全部进入合流等待后再放行
	close(release)
	wg.Wait()
	conflictNow := <-conflictCh
	replayCount := 0
	for _, o := range outs {
		if o.Replayed && bytes.Equal(o.Result, []byte("joined")) {
			replayCount++
		}
	}
	check("执行中同体并发合流到同一结果且只执行一次", replayCount == 8 && gw.ExecCalls() == 4)
	check("执行中异体立刻冲突不阻塞", gateway.IsConflict(conflictNow.Err))

	// 7. 到期（左闭右开）后重新执行
	clk.t = clk.t.Add(10 * time.Minute)
	info := gw.Lookup("k1")
	r1 := gw.Submit("k1", []byte("body"), func([]byte) ([]byte, error) { return []byte("new-era"), nil })
	check("恰好到期判过期且再提交真正重新执行", !info.Known && !r1.Replayed &&
		bytes.Equal(r1.Result, []byte("new-era")) && gw.ExecCalls() == 5)

	// 8. 过期不影响执行中的记录
	started2 := make(chan struct{}, 1)
	release2 := make(chan struct{})
	out8 := make(chan gateway.Outcome, 1)
	go func() {
		out8 <- gw.Submit("k8", []byte("b"), func([]byte) ([]byte, error) {
			started2 <- struct{}{}
			<-release2
			return []byte("still-ok"), nil
		})
	}()
	<-started2
	clk.t = clk.t.Add(time.Hour)
	running := gw.Lookup("k8")
	close(release2)
	o8 := <-out8
	check("过期不影响正在执行中的记录", running.Known && running.State.String() == "running" &&
		bytes.Equal(o8.Result, []byte("still-ok")) && o8.Err == nil)

	// 9. 不存在的键查询为零值
	check("不存在的键查询返回零值与不存在", gw.Lookup("never") == (gateway.Info{}))

	// 10. 长耗时执行期间别的键不被阻塞
	started3 := make(chan struct{}, 1)
	release3 := make(chan struct{})
	go gw.Submit("slow", []byte("b"), func([]byte) ([]byte, error) {
		started3 <- struct{}{}
		<-release3
		return []byte("slow"), nil
	})
	<-started3
	fastOK := make(chan bool, 1)
	go func() {
		o := gw.Submit("fast", []byte("b"), func([]byte) ([]byte, error) { return []byte("fast"), nil })
		fastOK <- (!o.Replayed && bytes.Equal(o.Result, []byte("fast")))
	}()
	check("长耗时执行期间其他键仍能正常完成", <-fastOK)
	close(release3)

	fmt.Printf("总计 %d/%d 步通过\n", pass, total)
	if pass != total {
		os.Exit(1)
	}
}
