package deploy

import (
	"errors"
	"sync"
	"testing"
)

func callerOf(name string, perms ...Perm) Caller {
	var p Perm
	for _, q := range perms {
		p |= q
	}
	return Caller{User: name, Perms: p}
}

func allPerms(name string) Caller {
	return callerOf(name, PermDeploy, PermApprove, PermRollback, PermAdmin)
}

func mustNew(t *testing.T, envs []EnvConfig) *Coordinator {
	t.Helper()
	c, err := New(envs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func mustReq(t *testing.T, c *Coordinator, now int64, env string, ver int64, rb bool, cl Caller) int64 {
	t.Helper()
	id, err := c.Request(now, env, ver, rb, cl)
	if err != nil {
		t.Fatalf("Request t=%d env=%s ver=%d rb=%v: %v", now, env, ver, rb, err)
	}
	return id
}

func mustApprove(t *testing.T, c *Coordinator, now int64, id int64, cl Caller) {
	t.Helper()
	if err := c.Approve(now, id, cl); err != nil {
		t.Fatalf("Approve t=%d id=%d by=%s: %v", now, id, cl.User, err)
	}
}

func mustStatus(t *testing.T, c *Coordinator, id int64, want Status) {
	t.Helper()
	s, err := c.Get(id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	if s.Status != want {
		t.Fatalf("id=%d status=%s, want %s", id, s.Status, want)
	}
}

// 例一：prod K=1 TTL=100 T=50 的超时级联与状态不符。
func TestSpecExampleTimeoutCascade(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "prod", K: 1, TTL: 100, T: 50}})
	alice, bob := allPerms("alice"), allPerms("bob")

	id1 := mustReq(t, c, 0, "prod", 5, false, alice)
	mustStatus(t, c, id1, StatusPending)
	mustApprove(t, c, 10, id1, bob)
	mustStatus(t, c, id1, StatusRunning)

	id2 := mustReq(t, c, 20, "prod", 6, false, alice)
	mustApprove(t, c, 25, id2, bob)
	mustStatus(t, c, id2, StatusQueued)

	err := c.Finish(130, id1, true, alice)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Finish after timeouts err=%v, want ErrInvalidState", err)
	}
	mustStatus(t, c, id1, StatusTimedOut)
	mustStatus(t, c, id2, StatusTimedOut)
	s2, _ := c.Get(id2)
	if s2.Start != 60 {
		t.Fatalf("id2 start=%d, want 60（以到期时刻授锁）", s2.Start)
	}
	if cur := c.envs["prod"].lock.Cur(); cur != 0 {
		t.Fatalf("cur=%d, want 0", cur)
	}
}

// TTL=35：g=60 时 2 号批准恰在 60 失效（25+35=60），转 Expired。
func TestTTLExpiresExactlyAtGrant(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "prod", K: 1, TTL: 35, T: 50}})
	alice, bob := allPerms("alice"), allPerms("bob")
	id1 := mustReq(t, c, 0, "prod", 5, false, alice)
	mustApprove(t, c, 10, id1, bob)
	id2 := mustReq(t, c, 20, "prod", 6, false, alice)
	mustApprove(t, c, 25, id2, bob)
	if err := c.Tick(130); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c, id1, StatusTimedOut)
	mustStatus(t, c, id2, StatusExpired)
}

// 早 1 秒：TTL=36 时 25+36=61，g=60 仍有效，2 号入锁后于 110 再超时。
func TestTTLValidOneSecondBeforeGrant(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "prod", K: 1, TTL: 36, T: 50}})
	alice, bob := allPerms("alice"), allPerms("bob")
	id1 := mustReq(t, c, 0, "prod", 5, false, alice)
	mustApprove(t, c, 10, id1, bob)
	id2 := mustReq(t, c, 20, "prod", 6, false, alice)
	mustApprove(t, c, 25, id2, bob)
	if err := c.Tick(130); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c, id1, StatusTimedOut)
	mustStatus(t, c, id2, StatusTimedOut)
}

// 例二：队列按转入 Queued 的次序 [3,2]，cur 越过 2 号版本后 Stale；回滚需多一票。
func TestSpecExampleStaleAndRollback(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "prod", K: 1, TTL: 1000, T: 1000}})
	alice, bob, carol := allPerms("alice"), allPerms("bob"), allPerms("carol")

	id1 := mustReq(t, c, 0, "prod", 5, false, alice)
	mustApprove(t, c, 0, id1, bob)
	id2 := mustReq(t, c, 1, "prod", 6, false, alice)
	id3 := mustReq(t, c, 2, "prod", 7, false, alice)
	mustApprove(t, c, 3, id3, bob)
	mustApprove(t, c, 4, id2, bob)

	if err := c.Finish(5, id1, true, alice); err != nil {
		t.Fatal(err)
	}
	if cur := c.envs["prod"].lock.Cur(); cur != 5 {
		t.Fatalf("cur=%d want 5", cur)
	}
	mustStatus(t, c, id3, StatusRunning)
	if err := c.Finish(6, id3, true, alice); err != nil {
		t.Fatal(err)
	}
	if cur := c.envs["prod"].lock.Cur(); cur != 7 {
		t.Fatalf("cur=%d want 7", cur)
	}
	mustStatus(t, c, id2, StatusStale)

	if _, err := c.Request(7, "prod", 7, true, alice); !errors.Is(err, ErrVersionStale) {
		t.Fatalf("rollback to cur err=%v want ErrVersionStale", err)
	}
	if _, err := c.Request(7, "prod", 6, true, alice); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("rollback to 6 err=%v want ErrUnknownVersion", err)
	}
	rb := mustReq(t, c, 8, "prod", 5, true, alice)
	mustStatus(t, c, rb, StatusPending)
	mustApprove(t, c, 9, rb, bob)
	mustStatus(t, c, rb, StatusPending)
	mustApprove(t, c, 10, rb, carol)
	mustStatus(t, c, rb, StatusRunning)
	if err := c.Finish(11, rb, true, alice); err != nil {
		t.Fatal(err)
	}
	if cur := c.envs["prod"].lock.Cur(); cur != 5 {
		t.Fatalf("cur=%d want 5 after rollback", cur)
	}
}

// Stale 先于 Expired：队首既过时又失效时仍判 Stale。
func TestStaleCheckedBeforeExpired(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 1, TTL: 10, T: 1000}})
	alice, bob := allPerms("alice"), allPerms("bob")
	id1 := mustReq(t, c, 0, "e", 9, false, alice)
	mustApprove(t, c, 0, id1, bob)
	stale := mustReq(t, c, 1, "e", 6, false, alice)
	fresh := mustReq(t, c, 1, "e", 10, false, alice)
	mustApprove(t, c, 1, stale, bob)  // at=1，g>=11 失效
	mustApprove(t, c, 15, fresh, bob) // at=15，g<25 有效
	if err := c.Finish(20, id1, true, alice); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c, stale, StatusStale)
	mustStatus(t, c, fresh, StatusRunning)
}

// 失效批准的重批：有效期间重复拒绝；失效后以新时刻替换。
func TestReapproveAfterExpiry(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 2, TTL: 100, T: 1000}})
	alice, bob, carol := allPerms("alice"), allPerms("bob"), allPerms("carol")
	id := mustReq(t, c, 0, "e", 5, false, alice)
	mustApprove(t, c, 0, id, bob)
	if err := c.Approve(50, id, bob); !errors.Is(err, ErrDuplicateApproval) {
		t.Fatalf("active duplicate err=%v", err)
	}
	mustApprove(t, c, 100, id, bob) // 旧票失效后以新时刻替换，仍只 1 票
	mustStatus(t, c, id, StatusPending)
	mustApprove(t, c, 100, id, carol)
	mustStatus(t, c, id, StatusRunning)
}

// 非属主取消：无 Admin 只能取消自己的请求；Admin 可取消他人请求。
func TestCancelOwnership(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 1, TTL: 1000, T: 1000}})
	alice := callerOf("alice", PermDeploy)
	bob := callerOf("bob", PermDeploy)
	root := callerOf("root", PermAdmin)
	id := mustReq(t, c, 0, "e", 5, false, alice)
	if err := c.Cancel(1, id, bob); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner cancel err=%v want ErrNotOwner", err)
	}
	mustStatus(t, c, id, StatusPending)
	if err := c.Cancel(2, id, root); err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	mustStatus(t, c, id, StatusCanceled)
}

// 取消 Running 后以 g=now 授锁，队列继续推进。
func TestCancelRunningGrantsAtNow(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 0, TTL: 1, T: 1000}})
	alice := allPerms("alice")
	id1 := mustReq(t, c, 0, "e", 5, false, alice)
	id2 := mustReq(t, c, 0, "e", 6, false, alice)
	mustStatus(t, c, id1, StatusRunning)
	mustStatus(t, c, id2, StatusQueued)
	if err := c.Cancel(10, id1, alice); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c, id1, StatusCanceled)
	mustStatus(t, c, id2, StatusRunning)
	s2, _ := c.Get(id2)
	if s2.Start != 10 {
		t.Fatalf("id2 start=%d want 10", s2.Start)
	}
}

// 状态类拒绝发生时，时钟推进与超时落地仍已生效。
func TestStateRejectionStillLandsTimeouts(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 1, TTL: 1000, T: 50}})
	alice, bob := allPerms("alice"), allPerms("bob")
	id1 := mustReq(t, c, 0, "e", 5, false, alice)
	mustApprove(t, c, 0, id1, bob)
	id2 := mustReq(t, c, 0, "e", 9, false, alice)
	err := c.Finish(100, id2, true, alice)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("err=%v want ErrInvalidState", err)
	}
	mustStatus(t, c, id1, StatusTimedOut)
	if c.now != 100 {
		t.Fatalf("now=%d want 100（拒绝后时钟仍推进）", c.now)
	}
}

// 自批：请求人不能批准自己的请求。
func TestSelfApproval(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 1, TTL: 100, T: 1000}})
	alice := allPerms("alice")
	id := mustReq(t, c, 0, "e", 5, false, alice)
	if err := c.Approve(0, id, alice); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("err=%v want ErrSelfApproval", err)
	}
}

// K=0：直接 Queued、空闲立即授锁；同刻同版本重复请求报版本过时。
func TestZeroApprovalsImmediateGrant(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 0, TTL: 1, T: 100}})
	alice := allPerms("alice")
	id1 := mustReq(t, c, 0, "e", 5, false, alice)
	mustStatus(t, c, id1, StatusRunning)
	if err := c.Finish(1, id1, true, alice); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Request(2, "e", 5, false, alice); !errors.Is(err, ErrVersionStale) {
		t.Fatalf("re-request ver<=cur err=%v want ErrVersionStale", err)
	}
}

// 拒绝次序：参数非法优先于时钟回退，且不改任何状态；时钟回退优先于无权限。
func TestRejectionOrder(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 0, TTL: 1, T: 100}})
	if err := c.Tick(10); err != nil {
		t.Fatal(err)
	}
	// 参数非法先于时钟回退：非法 now 直接报参数，状态与时钟不变。
	if err := c.Tick(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err=%v want ErrInvalidArgument", err)
	}
	if c.now != 10 {
		t.Fatalf("now=%d want 10", c.now)
	}
	// 时钟回退先于无权限：Deploy 权限缺失也要先报时钟回退。
	weak := callerOf("weak")
	if _, err := c.Request(5, "e", 3, false, weak); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("err=%v want ErrClockBackwards", err)
	}
	// 无权限先于环境不存在。
	if _, err := c.Request(10, "ghost", 3, false, weak); !errors.Is(err, ErrPermission) {
		t.Fatalf("err=%v want ErrPermission", err)
	}
	// 环境不存在先于版本类检查。
	if _, err := c.Request(10, "ghost", 3, false, allPerms("a")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	// 编号不存在先于状态不符（Finish 一个不存在的 id）。
	if err := c.Finish(10, 999, true, allPerms("a")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	// 自批先于重复批准（K=2，一票不足以入队，请求保持 Pending）。
	c2 := mustNew(t, []EnvConfig{{Name: "p", K: 2, TTL: 1000, T: 1000}})
	id := mustReq(t, c2, 10, "p", 7, false, allPerms("alice"))
	if err := c2.Approve(10, id, allPerms("bob")); err != nil {
		t.Fatalf("approve by bob: %v", err)
	}
	if err := c2.Approve(11, id, allPerms("alice")); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("err=%v want ErrSelfApproval", err)
	}
}

// Get 不做虚拟推进：t=100 时 Running 已到期但 Get 仍报 Running，直到 Tick/任意操作落地。
func TestGetDoesNotAdvance(t *testing.T) {
	c := mustNew(t, []EnvConfig{{Name: "e", K: 0, TTL: 1, T: 50}})
	id := mustReq(t, c, 0, "e", 5, false, allPerms("alice"))
	mustStatus(t, c, id, StatusRunning)
	// 不调任何落地操作，直接观察内部仍未推进。
	s, err := c.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != StatusRunning {
		t.Fatalf("Get without landing status=%s want Running", s.Status)
	}
	if err := c.Tick(60); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c, id, StatusTimedOut)
}
func TestConcurrentSafety(t *testing.T) {
	c := mustNew(t, []EnvConfig{
		{Name: "e1", K: 1, TTL: 50, T: 30},
		{Name: "e2", K: 0, TTL: 1, T: 30},
	})
	users := []Caller{
		callerOf("a", PermDeploy|PermApprove|PermRollback|PermAdmin),
		callerOf("b", PermDeploy|PermApprove|PermAdmin),
		callerOf("d", PermDeploy),
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := int64(w * 7)
			for i := 0; i < 200; i++ {
				now := base + int64(i)
				cl := users[i%len(users)]
				switch i % 7 {
				case 0:
					_, _ = c.Request(now, "e1", int64(i+1), false, cl)
				case 1:
					_, _ = c.Request(now, "e2", int64(i+1), false, cl)
				case 2:
					_ = c.Approve(now, int64(1+i%6), cl)
				case 3:
					_ = c.Finish(now, int64(1+i%6), i%2 == 0, cl)
				case 4:
					_ = c.Cancel(now, int64(1+i%6), cl)
				case 5:
					_ = c.Tick(now)
				default:
					_, _ = c.Get(int64(1 + i%8))
				}
			}
		}(w)
	}
	wg.Wait()
	for _, name := range []string{"e1", "e2"} {
		running := 0
		for _, r := range c.reqs {
			if r.status == StatusRunning && r.env.lock.Name() == name {
				running++
			}
		}
		if running > 1 {
			t.Fatalf("env=%s 同时有 %d 个 Running", name, running)
		}
	}
	// 被拒不占号：已接受编号应恰为 1..seq。
	for id := int64(1); id <= c.seq; id++ {
		if _, err := c.Get(id); err != nil {
			t.Fatalf("seq 编号空洞 id=%d: %v", id, err)
		}
	}
}
