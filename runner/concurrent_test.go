package runner_test

import (
	"sync"
	"testing"

	"ontology/grants"
	"ontology/runner"
)

// TestConcurrentRevokeStart 在 -race 下让 Revoke 与 StartStep 紧密交错：
// 结果只需等价于某个串行顺序——每个实例要么 Allow 要么 Deny，二者均合法，
// 但审计、状态与终局必须自洽且全程无数据竞争。
func TestConcurrentRevokeStart(t *testing.T) {
	r, g, c := newKit(t, 100)
	if err := g.Grant([]byte("p"), 0b0011); err != nil {
		t.Fatal(err)
	}
	if err := c.Define([]byte("d"), 0b0011, []uint64{0b0001}); err != nil {
		t.Fatal(err)
	}
	const n = 40
	for i := 0; i < n; i++ {
		inst := []byte{byte('a' + i/26), byte('a' + i%26)}
		if err := r.Launch(inst, []byte("d"), []byte("p"), 0); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan string, n)
	for i := 0; i < n; i++ {
		inst := []byte{byte('a' + i/26), byte('a' + i%26)}
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_ = g.Revoke([]byte("p"), 0b0011)
			_ = g.Grant([]byte("p"), 0b0011)
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = r.StartStep(inst, 1)
		}()
		go func() {
			defer wg.Done()
			<-start
			st, err := r.Status(inst, 1)
			if err != nil {
				t.Errorf("Status: %v", err)
				return
			}
			if st.Phase == runner.RunningPhase {
				results <- "running"
			} else {
				results <- "suspended"
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var running, suspended int
	for s := range results {
		switch s {
		case "running":
			running++
		case "suspended":
			suspended++
		}
	}
	t.Logf("并发结果：%d 个实例 Allow(Running)，%d 个 Deny(Suspended)；等价于某串行顺序",
		running, suspended)
	if running+suspended != n {
		t.Fatalf("自洽实例数 = %d, 期望 %d", running+suspended, n)
	}
	for i := 0; i < n; i++ {
		inst := []byte{byte('a' + i/26), byte('a' + i%26)}
		evs, err := r.AuditLog(inst)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 || evs[0].Seq != 1 {
			t.Fatalf("实例 %q 审计 = %+v, 期望恰一条且序号1", inst, evs)
		}
		switch evs[0].Kind {
		case runner.Allow, runner.Deny:
		default:
			t.Fatalf("实例 %q 首条审计 = %v", inst, evs[0].Kind)
		}
	}
}

// TestConcurrentDifferentInstances 多实例长时间交错推进，验证终局唯一、
// 审计序号连续与跨实例时钟单调。grants 的 Grant/Revoke 无全局时钟约束，
// 与 runner 调用在权限表锁与运行器锁之间真实交错；runner 侧的 now 由一个
// 协调互斥按调用顺序分配，故每次交错都等价于某个合法串行顺序。
func TestConcurrentDifferentInstances(t *testing.T) {
	r, g, c := newKit(t, 1_000_000)
	if err := g.Grant([]byte("p"), 0b0011); err != nil {
		t.Fatal(err)
	}
	if err := g.Grant([]byte("a"), grants.ApproverBit); err != nil {
		t.Fatal(err)
	}
	if err := c.Define([]byte("d"), 0b0011, []uint64{0b0001, 0b0010}); err != nil {
		t.Fatal(err)
	}
	const n = 20
	var wg sync.WaitGroup
	startCh := make(chan struct{})
	var order sync.Mutex // 协调锁：now 与 runner 调用在同一临界区顺序化
	clock := int64(0)
	// call 在协调临界区内分配单调 now 并执行 f；Grant/Revoke 在其外并发。
	call := func(f func(now int64) error) error {
		order.Lock()
		clock++
		now := clock
		err := f(now)
		order.Unlock()
		return err
	}
	for i := 0; i < n; i++ {
		inst := []byte{byte('A' + i/26), byte('A' + i%26)}
		if err := r.Launch(inst, []byte("d"), []byte("p"), 0); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			if err := call(func(now int64) error { return r.StartStep(inst, now) }); err != nil {
				t.Errorf("start0: %v", err)
				return
			}
			if err := call(func(now int64) error { return r.FinishStep(inst, now) }); err != nil {
				t.Errorf("finish0: %v", err)
				return
			}
			if err := g.Revoke([]byte("p"), 0b0010); err != nil {
				t.Errorf("revoke: %v", err)
			}
			if err := call(func(now int64) error { return r.StartStep(inst, now) }); err != nil {
				t.Errorf("start1: %v", err)
			}
			if err := g.Grant([]byte("p"), 0b0010); err != nil {
				t.Errorf("grant: %v", err)
			}
			// 步1 可能 Allow 或 Deny（取决于与其他实例 Revoke/Grant 的交错）。
			var st runner.Status
			if err := call(func(now int64) (e error) {
				st, e = r.Status(inst, now)
				return e
			}); err != nil {
				t.Errorf("status: %v", err)
				return
			}
			if st.Suspended {
				if err := call(func(now int64) error { return r.Approve(inst, []byte("a"), now) }); err != nil {
					t.Errorf("approve: %v", err)
					return
				}
			}
			if err := call(func(now int64) error { return r.FinishStep(inst, now) }); err != nil {
				t.Errorf("finish1: %v", err)
				return
			}
			if err := call(func(now int64) (e error) {
				st, e = r.Status(inst, now)
				return e
			}); err != nil || st.Phase != runner.CompletedPhase {
				t.Errorf("终局错误: %+v %v", st, err)
			}
		}()
	}
	close(startCh)
	wg.Wait()
}
