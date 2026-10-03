package registry

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/acl"
	"ontology/policy"
)

func newTest(t *testing.T, R int64, N int) *Registry {
	t.Helper()
	g, err := New(R, N, acl.New())
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return g
}

// TestSpecExample 完整复现题目给出的示例。
func TestSpecExample(t *testing.T) {
	g := newTest(t, 100, 1000)
	w, p1, p2 := []byte("w"), []byte("p1"), []byte("p2")

	run, err := g.Start(w, p1, policy.Reject, policy.Fail, 0)
	t.Logf("Start(w,p1,Reject,Fail,0) -> run=%d err=%v 依据: 无记录新建", run, err)
	if run != 1 || err != nil {
		t.Fatalf("want run 1, got %d,%v", run, err)
	}
	if err = g.Finish(w, 1, policy.Completed, 10); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	for _, now := range []int64{50, 109} {
		if _, err = g.Start(w, p1, policy.Reject, policy.Fail, now); !errors.Is(err, ErrReuse) {
			t.Fatalf("now=%d Reject want ErrReuse, got %v", now, err)
		}
		if _, err = g.Start(w, p1, policy.AllowFailedOnly, policy.Fail, now); !errors.Is(err, ErrReuse) {
			t.Fatalf("now=%d Completed+AllowFailedOnly want ErrReuse, got %v", now, err)
		}
		t.Logf("now=%d: Reject 与 AllowFailedOnly(Completed) 均 ErrReuse", now)
	}
	run, err = g.Start(w, p1, policy.Reject, policy.Fail, 110)
	t.Logf("now=110 恰等过期 -> run=%d err=%v 依据: end+R=110，视同不存在", run, err)
	if run != 2 || err != nil {
		t.Fatalf("want run 2, got %d,%v", run, err)
	}
	if n, _ := g.Count(110); n != 1 {
		t.Fatalf("Count 不应含已过期者, got %d", n)
	}
	if _, err = g.Start(w, p1, policy.AllowAll, policy.Fail, 120); !errors.Is(err, ErrRunning) {
		t.Fatalf("want ErrRunning, got %v", err)
	}
	if _, err = g.Start(w, p2, policy.AllowAll, policy.Terminate, 120); !errors.Is(err, ErrDenied) {
		t.Fatalf("p2 want ErrDenied, got %v", err)
	}
	run, err = g.Start(w, p1, policy.AllowAll, policy.Terminate, 120)
	t.Logf("p1 Terminate -> run=%d err=%v 依据: 所有者可终止，绕过 reuse/容量", run, err)
	if run != 3 || err != nil {
		t.Fatalf("want run 3, got %d,%v", run, err)
	}
	if g.live["w"].run != 3 || g.live["w"].state != policy.Running {
		t.Fatal("当前记录应为 run3 Running")
	}
	if err = g.Finish(w, 2, policy.Failed, 121); !errors.Is(err, ErrStale) {
		t.Fatalf("run2 已被替换, want ErrStale, got %v", err)
	}
}

// setupCondition 构造七种现状之一，返回 registry 与现状下已有 run 的编号。
func setupCondition(t *testing.T, cond string) (*Registry, uint64) {
	t.Helper()
	g := newTest(t, 100, 1000)
	id := []byte("id")
	if cond == "absent" {
		return g, 0
	}
	run, _ := g.Start(id, []byte("p"), policy.Reject, policy.Fail, 0)
	switch cond {
	case "running":
		return g, run
	case "expired":
		if err := g.Finish(id, run, policy.Completed, 10); err != nil {
			t.Fatal(err)
		}
	case "completed", "failed", "cancelled":
		st := map[string]policy.State{
			"completed": policy.Completed, "failed": policy.Failed, "cancelled": policy.Cancelled,
		}[cond]
		if err := g.Finish(id, run, st, 10); err != nil {
			t.Fatal(err)
		}
	case "terminated":
		// 白盒置入 Terminated 现状（真实 Terminate 会同槽立即替换，旧态无法直接观察）。
		g.live["id"].state = policy.Terminated
		g.live["id"].endAt = 10
	}
	return g, run
}

// TestStartMatrix 九种策略组合 × 七种现状的结果表（主体恒为所有者 p）。
func TestStartMatrix(t *testing.T) {
	conds := []struct {
		name string
		at   int64
	}{
		{"running", 50}, {"completed", 50}, {"failed", 50}, {"cancelled", 50},
		{"terminated", 50}, {"expired", 110}, {"absent", 50},
	}
	for _, cd := range conds {
		for reuse := policy.AllowAll; reuse <= policy.Reject; reuse++ {
			for conflict := policy.Fail; conflict <= policy.Terminate; conflict++ {
				g, oldRun := setupCondition(t, cd.name)
				got, err := g.Start([]byte("id"), []byte("p"), reuse, conflict, cd.at)
				var wantErr error
				var wantRun uint64
				switch {
				case cd.name == "running" && conflict == policy.Fail:
					wantErr = ErrRunning
				case cd.name == "running" && conflict == policy.UseExisting:
					wantRun = oldRun
				case cd.name == "running": // Terminate：调用方是所有者
					wantRun = oldRun + 1
				case cd.name == "completed" && reuse != policy.AllowAll:
					wantErr = ErrReuse
				case isFailureState(cd.name) && reuse == policy.Reject:
					wantErr = ErrReuse
				default: // expired/absent 视同新建；failed 类 + AllowAll/AllowFailedOnly 替换
					if cd.name == "absent" {
						wantRun = 1
					} else {
						wantRun = oldRun + 1
					}
				}
				reason := fmt.Sprintf("现状=%s reuse=%d conflict=%d", cd.name, reuse, conflict)
				t.Logf("input %s -> output run=%d err=%v；期望 run=%d err=%v",
					reason, got, err, wantRun, wantErr)
				if !errors.Is(err, wantErr) || (wantErr == nil && got != wantRun) {
					t.Fatalf("%s: got (%d,%v), want (%d,%v)", reason, got, err, wantRun, wantErr)
				}
			}
		}
	}
}

func isFailureState(cond string) bool {
	return cond == "failed" || cond == "cancelled" || cond == "terminated"
}

// TestConcurrentStart 100 goroutine 对同一 id 并发 Start。
func TestConcurrentStart(t *testing.T) {
	for _, co := range []policy.ConflictPolicy{policy.Fail, policy.UseExisting} {
		g := newTest(t, 100, 1000)
		var wg sync.WaitGroup
		var lock sync.Mutex
		res := map[uint64]int{}
		var errs []error
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := g.Start([]byte("id"), []byte("p"), policy.Reject, co, 0)
				lock.Lock()
				if err != nil {
					errs = append(errs, err)
				} else {
					res[r]++
				}
				lock.Unlock()
			}()
		}
		wg.Wait()
		if co == policy.Fail {
			if res[1] != 1 || len(errs) != 99 {
				t.Fatalf("Fail: res=%v errs=%d", res, len(errs))
			}
			for _, e := range errs {
				if !errors.Is(e, ErrRunning) {
					t.Fatalf("并发冲突应全 ErrRunning, got %v", e)
				}
			}
		} else if res[1] != 100 || len(errs) != 0 {
			t.Fatalf("UseExisting: res=%v errs=%d", res, len(errs))
		}
		if g.nextRun != 1 {
			t.Fatalf("只应新建一次, nextRun=%d", g.nextRun)
		}
		t.Logf("conflict=%d 并发100: res=%v errs=%d nextRun=%d", co, res, len(errs), g.nextRun)
	}
}
