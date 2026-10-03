package registry

import (
	"errors"
	"fmt"
	"testing"

	"ontology/acl"
	"ontology/policy"
)

// TestACL 覆盖 acl 的 Grant/Revoke 与“所有者或 admin 才可终止”规则。
func TestACL(t *testing.T) {
	a := acl.New()
	p1, p2, boss := []byte("p1"), []byte("p2"), []byte("boss")
	if !a.CanTerminate(p1, p1) || a.CanTerminate(p2, p1) {
		t.Fatal("所有者可终止；非所有者非 admin 不可终止")
	}
	if err := a.Grant(boss); err != nil || !a.CanTerminate(boss, p1) {
		t.Fatalf("Grant 后 admin 应可终止, err=%v", err)
	}
	if err := a.Revoke(boss); err != nil || a.CanTerminate(boss, p1) {
		t.Fatalf("Revoke 后权限应消失, err=%v", err)
	}
	if err := a.Grant(nil); !errors.Is(err, acl.ErrEmptyPrincipal) {
		t.Fatalf("空主体 want ErrEmptyPrincipal, got %v", err)
	}
	t.Logf("ACL: owner/admin/空主体判定均符合预期")
}

// TestExpiryCapacityClock 过期恰等/差1、容量与替换路径、时钟、Finish 错误顺序、拒绝不耗号。
func TestExpiryCapacityClock(t *testing.T) {
	g := newTest(t, 100, 1)
	id := []byte("id")
	run, _ := g.Start(id, []byte("p"), policy.Reject, policy.Fail, 0)
	if err := g.Finish(id, run, policy.Completed, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Start(id, []byte("p"), policy.Reject, policy.Fail, 109); !errors.Is(err, ErrReuse) {
		t.Fatalf("109 差1未过期 want ErrReuse, got %v", err)
	}
	run2, err := g.Start(id, []byte("p"), policy.Reject, policy.Fail, 110)
	if err != nil || run2 != 2 {
		t.Fatalf("110 恰等过期应新建 run2, got %d,%v", run2, err)
	}
	if _, err = g.Start([]byte("other"), []byte("p"), policy.Reject, policy.Fail, 110); !errors.Is(err, ErrCapacity) {
		t.Fatalf("不同 ID 在 N=1 新建 want ErrCapacity, got %v", err)
	}
	run3, err := g.Start(id, []byte("p"), policy.AllowAll, policy.Terminate, 120)
	if err != nil || run3 != 3 {
		t.Fatalf("Terminate 替换 Running 不查容量 want run3, got %d,%v", run3, err)
	}
	if err = g.Finish(id, run3, policy.Failed, 125); err != nil {
		t.Fatal(err)
	}
	run4, err := g.Start(id, []byte("p"), policy.AllowFailedOnly, policy.Fail, 130)
	if err != nil || run4 != 4 {
		t.Fatalf("已结束复用替换不查容量 want run4, got %d,%v", run4, err)
	}
	if _, err = g.Start(id, []byte("p"), policy.Reject, policy.Fail, 119); !errors.Is(err, ErrClock) {
		t.Fatalf("时钟回拨 want ErrClock, got %v", err)
	}
	if n, err := g.Count(119); !errors.Is(err, ErrClock) || n != 0 {
		t.Fatalf("Count 回拨 want ErrClock, got %d,%v", n, err)
	}
	if n, _ := g.Count(130); n != 1 {
		t.Fatalf("Count 只读不改状态, got %d", n)
	}
	if g.nextRun != 4 {
		t.Fatalf("被拒绝操作不应消耗 run 号, nextRun=%d", g.nextRun)
	}
	if err = g.Finish([]byte("nope"), 1, policy.Failed, 131); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err = g.Finish(id, 99, policy.Failed, 131); !errors.Is(err, ErrStale) {
		t.Fatalf("want ErrStale, got %v", err)
	}
	if err = g.Finish(id, run4, policy.Completed, 131); err != nil {
		t.Fatalf("run4 Running, Finish 应成功, got %v", err)
	}
	if err = g.Finish(id, run4, policy.Failed, 132); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("已结束 want ErrNotRunning, got %v", err)
	}
	if err = g.Finish(id, run4, policy.Terminated, 132); !errors.Is(err, ErrArgument) {
		t.Fatalf("Finish(Terminated) want ErrArgument, got %v", err)
	}
}

// TestTerminatePermission 核验 Terminate 权限与 UseExisting 不查权限。
func TestTerminatePermission(t *testing.T) {
	g := newTest(t, 100, 10)
	id, p1, p2, boss := []byte("id"), []byte("p1"), []byte("p2"), []byte("boss")
	run, _ := g.Start(id, p1, policy.Reject, policy.Fail, 0)
	if r, err := g.Start(id, p2, policy.Reject, policy.UseExisting, 10); err != nil || r != run {
		t.Fatalf("UseExisting 不查权限, got %d,%v", r, err)
	}
	t.Logf("UseExisting p2 -> run=%d 依据: 幂等启动不改变现状", run)
	if _, err := g.Start(id, p2, policy.Reject, policy.Terminate, 20); !errors.Is(err, ErrDenied) {
		t.Fatalf("p2 want ErrDenied, got %v", err)
	}
	if g.nextRun != run {
		t.Fatal("ErrDenied 不应消耗 run 号")
	}
	if err := g.acl.Grant(boss); err != nil {
		t.Fatal(err)
	}
	r2, err := g.Start(id, boss, policy.Reject, policy.Terminate, 20)
	if err != nil || r2 != run+1 {
		t.Fatalf("admin Terminate want run %d, got %d,%v", run+1, r2, err)
	}
	if err := g.acl.Revoke(boss); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Start(id, p2, policy.Reject, policy.Terminate, 30); !errors.Is(err, ErrDenied) {
		t.Fatalf("Revoke 后 p2 want ErrDenied, got %v", err)
	}
}

// TestInvalidArgs 参数非法优先于一切，且不改变状态。
func TestInvalidArgs(t *testing.T) {
	if _, err := New(0, 10, nil); !errors.Is(err, ErrArgument) {
		t.Fatalf("R=0 want ErrArgument, got %v", err)
	}
	if _, err := New(1, 0, nil); !errors.Is(err, ErrArgument) {
		t.Fatalf("N=0 want ErrArgument, got %v", err)
	}
	g := newTest(t, 100, 10)
	try := func(name string, f func() error) {
		if err := f(); !errors.Is(err, ErrArgument) {
			t.Fatalf("%s want ErrArgument, got %v", name, err)
		}
	}
	for _, c := range []struct {
		name   string
		id, p  []byte
		ru, co int
		now    int64
	}{
		{"空id", nil, []byte("p"), 0, 0, 0},
		{"空主体", []byte("x"), nil, 0, 0, 0},
		{"reuse越界", []byte("x"), []byte("p"), 99, 0, 0},
		{"conflict越界", []byte("x"), []byte("p"), 0, 99, 0},
		{"now负", []byte("x"), []byte("p"), 0, 0, -1},
		{"now超大", []byte("x"), []byte("p"), 0, 0, maxNow + 1},
	} {
		ru, co := policy.ReusePolicy(c.ru), policy.ConflictPolicy(c.co)
		try(c.name, func() error { _, e := g.Start(c.id, c.p, ru, co, c.now); return e })
	}
	if g.nextRun != 0 || g.clock != 0 {
		t.Fatalf("非法参数不应改变状态: nextRun=%d clock=%d", g.nextRun, g.clock)
	}
}

// TestPeepBound 一次操作考察堆项数 ≤ 到期项数+1（1 与 10000 个无关 ID 两档）。
func TestPeepBound(t *testing.T) {
	for _, n := range []int{1, 10000} {
		g := newTest(t, 100, n+10)
		for i := 0; i < n; i++ {
			id := []byte(fmt.Sprintf("u%05d", i))
			run, _ := g.Start(id, []byte("p"), policy.Reject, policy.Fail, 0)
			if err := g.Finish(id, run, policy.Completed, 0); err != nil {
				t.Fatal(err)
			}
		}
		g.peep = 0
		if _, err := g.Count(100); err != nil {
			t.Fatal(err)
		}
		t.Logf("无关ID=%d: 考察堆项=%d 上界=%d", n, g.peep, n+1)
		if int(g.peep) > n+1 {
			t.Fatalf("考察 %d 超过上界 %d", g.peep, n+1)
		}
	}
	g := newTest(t, 100, 10)
	r, _ := g.Start([]byte("u"), []byte("p"), policy.Reject, policy.Fail, 0)
	if err := g.Finish([]byte("u"), r, policy.Completed, 50); err != nil {
		t.Fatal(err)
	}
	g.peep = 0
	if _, err := g.Start([]byte("fresh"), []byte("p"), policy.Reject, policy.Fail, 60); err != nil {
		t.Fatal(err)
	}
	if g.peep != 1 {
		t.Fatalf("0 到期项时应只考察堆顶 1 项, got %d", g.peep)
	}
}
