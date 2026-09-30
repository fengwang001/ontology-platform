package deadlock

import (
	"bytes"
	"fmt"
	"log"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestManager(buf *bytes.Buffer) (*Manager, *ManualNetwork) {
	m := NewManager(log.New(buf, "", 0))
	net := NewManualNetwork(m.Deliver)
	m.SetNetwork(net)
	return m, net
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func grant(t *testing.T, m *Manager, txn, site, lock string, mode Mode) {
	t.Helper()
	ok, err := m.Request(txn, site, lock, mode)
	if err != nil {
		t.Fatalf("request %s %s/%s: %v", txn, site, lock, err)
	}
	if !ok {
		t.Fatalf("request %s %s/%s unexpectedly queued", txn, site, lock)
	}
}

func queue(t *testing.T, m *Manager, txn, site, lock string, mode Mode) {
	t.Helper()
	ok, err := m.Request(txn, site, lock, mode)
	if err != nil {
		t.Fatalf("request %s %s/%s: %v", txn, site, lock, err)
	}
	if ok {
		t.Fatalf("request %s %s/%s unexpectedly granted", txn, site, lock)
	}
}

// setupRing 构造三站点三事务环：T1->T2->T3->T1，开始时间戳 1<2<3。
func setupRing(t *testing.T, m *Manager) {
	t.Helper()
	for _, s := range []string{"S1", "S2", "S3"} {
		m.AddSite(s)
	}
	for _, l := range []struct{ site, lock string }{{"S1", "LA"}, {"S2", "LB"}, {"S3", "LC"}} {
		must(t, m.AddLock(l.site, l.lock))
	}
	must(t, m.Begin("T1", 1))
	must(t, m.Begin("T2", 2))
	must(t, m.Begin("T3", 3))
	grant(t, m, "T1", "S1", "LA", Exclusive)
	grant(t, m, "T2", "S2", "LB", Exclusive)
	grant(t, m, "T3", "S3", "LC", Exclusive)
	queue(t, m, "T1", "S2", "LB", Exclusive)
	queue(t, m, "T2", "S3", "LC", Exclusive)
	queue(t, m, "T3", "S1", "LA", Exclusive)
}

// 三站点三事务成环：真实死锁被发现，牺牲者为开始时间戳最大者 T3。
func TestThreeSiteRing(t *testing.T) {
	var buf bytes.Buffer
	m, net := newTestManager(&buf)
	setupRing(t, m)

	if got := m.WaitsFor("T1"); !reflect.DeepEqual(got, []string{"T2"}) {
		t.Fatalf("T1 waits=%v, want [T2]", got)
	}
	net.DeliverAll()

	if got := m.Victims(); !reflect.DeepEqual(got, []string{"T3"}) {
		t.Fatalf("victims=%v, want [T3]", got)
	}
	if s := m.TxnState("T3"); s != "aborted" {
		t.Fatalf("T3 state=%s, want aborted", s)
	}
	if m.HasCycle() {
		t.Fatal("wait cycle still exists after all deliveries")
	}
	if net.Pending() != 0 {
		t.Fatalf("pending=%d, want 0", net.Pending())
	}
	out := buf.String()
	for _, want := range []string{
		"op=Request txn=T1", "result=queued",
		"deadlock=confirmed", "victim=T3", "reason=max-start-ts",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q\nlog:\n%s", want, out)
		}
	}
	t.Logf("log:\n%s", out)
}

// 共享锁多持有者参与的环：T1、T2 共享持有 LA，T3 排他请求 LA 形成
// T1->T3->T1 环；T2 的排他请求排在 T1 之后，等待持有者与前排等待者。
func TestSharedHoldersRing(t *testing.T) {
	var buf bytes.Buffer
	m, net := newTestManager(&buf)
	m.AddSite("S1")
	m.AddSite("S2")
	must(t, m.AddLock("S1", "LA"))
	must(t, m.AddLock("S2", "LB"))
	must(t, m.Begin("T1", 1))
	must(t, m.Begin("T2", 2))
	must(t, m.Begin("T3", 3))

	grant(t, m, "T1", "S1", "LA", Shared)
	grant(t, m, "T2", "S1", "LA", Shared)
	grant(t, m, "T3", "S2", "LB", Exclusive)
	queue(t, m, "T1", "S2", "LB", Exclusive)
	queue(t, m, "T3", "S1", "LA", Exclusive)
	queue(t, m, "T2", "S2", "LB", Exclusive)

	if got := m.WaitsFor("T3"); !reflect.DeepEqual(got, []string{"T1", "T2"}) {
		t.Fatalf("T3 waits=%v, want [T1 T2]", got)
	}
	if got := m.WaitsFor("T2"); !reflect.DeepEqual(got, []string{"T1", "T3"}) {
		t.Fatalf("T2 waits=%v, want [T1 T3]", got)
	}
	net.DeliverAll()

	if got := m.Victims(); !reflect.DeepEqual(got, []string{"T3"}) {
		t.Fatalf("victims=%v, want [T3]", got)
	}
	if m.HasCycle() {
		t.Fatal("wait cycle still exists after all deliveries")
	}
	t.Logf("log:\n%s", buf.String())
}

// 等待解除后在途探测被丢弃：T2 的探测还在网络中，T1 已释放锁，
// 投递时所沿等待边不存在，探测丢弃且不误判。
func TestResolvedWaitDropsInFlightProbe(t *testing.T) {
	var buf bytes.Buffer
	m, net := newTestManager(&buf)
	m.AddSite("S1")
	must(t, m.AddLock("S1", "LA"))
	must(t, m.Begin("T1", 1))
	must(t, m.Begin("T2", 2))

	grant(t, m, "T1", "S1", "LA", Exclusive)
	queue(t, m, "T2", "S1", "LA", Exclusive)
	if net.Pending() != 1 {
		t.Fatalf("pending=%d, want 1", net.Pending())
	}
	must(t, m.Release("T1", "S1", "LA"))
	if s := m.TxnState("T2"); s != "active" {
		t.Fatalf("T2 state=%s, want active after grant", s)
	}
	net.DeliverAll()

	if got := m.Victims(); len(got) != 0 {
		t.Fatalf("victims=%v, want none", got)
	}
	if !strings.Contains(buf.String(), "reason=edge-gone") {
		t.Fatalf("log missing edge-gone drop\nlog:\n%s", buf.String())
	}
	t.Logf("log:\n%s", buf.String())
}

// 两个发起者发现同一环：乱序（逆序）投递下也只中止一个事务，
// 后到探测因等待边已消失被丢弃。
func TestTwoInitiatorsSameRingSingleVictim(t *testing.T) {
	var buf bytes.Buffer
	m, net := newTestManager(&buf)
	setupRing(t, m)

	for net.Pending() > 0 {
		net.Deliver(net.Pending() - 1)
	}

	if got := m.Victims(); !reflect.DeepEqual(got, []string{"T3"}) {
		t.Fatalf("victims=%v, want exactly [T3]", got)
	}
	if m.HasCycle() {
		t.Fatal("wait cycle still exists after all deliveries")
	}
	out := buf.String()
	if strings.Count(out, "deadlock=confirmed") != 1 {
		t.Fatalf("want exactly 1 confirmed deadlock\nlog:\n%s", out)
	}
	if !strings.Contains(out, "reason=stale-cycle") && !strings.Contains(out, "reason=edge-gone") {
		t.Fatalf("log missing drop of second initiator's probe\nlog:\n%s", out)
	}
	t.Logf("log:\n%s", out)
}

// 不含发起者的环：T1 的探测进入 T2<->T3 环后到达已在序列中的
// 非发起者事务即丢弃，不无限转发；环由 T2/T3 自己的探测发现。
func TestCycleWithoutInitiatorNotForwardedForever(t *testing.T) {
	var buf bytes.Buffer
	m, net := newTestManager(&buf)
	for _, s := range []string{"S1", "S2", "S3"} {
		m.AddSite(s)
	}
	must(t, m.AddLock("S1", "LA"))
	must(t, m.AddLock("S2", "LB"))
	must(t, m.AddLock("S3", "LC"))
	must(t, m.Begin("T1", 1))
	must(t, m.Begin("T2", 2))
	must(t, m.Begin("T3", 3))

	grant(t, m, "T1", "S1", "LA", Exclusive)
	grant(t, m, "T2", "S2", "LB", Exclusive)
	grant(t, m, "T3", "S3", "LC", Exclusive)
	queue(t, m, "T2", "S3", "LC", Exclusive)
	queue(t, m, "T3", "S2", "LB", Exclusive)
	queue(t, m, "T1", "S2", "LB", Exclusive)

	// 投递 T1 发起的全部探测：进入 T2<->T3 环后到达已在序列中的
	// 非发起者事务即丢弃，转发必然终止。
	for {
		idx := -1
		for i, msg := range net.PendingMessages() {
			if msg.Probe.Initiator == "T1" {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		net.Deliver(idx)
	}

	if got := m.Victims(); len(got) != 0 {
		t.Fatalf("T1's probe must not abort anyone, victims=%v", got)
	}
	if !strings.Contains(buf.String(), "reason=cycle-without-initiator") {
		t.Fatalf("log missing cycle-without-initiator drop\nlog:\n%s", buf.String())
	}
	net.DeliverAll()

	if got := m.Victims(); !reflect.DeepEqual(got, []string{"T3"}) {
		t.Fatalf("victims=%v, want [T3]", got)
	}
	if s := m.TxnState("T1"); s != "waiting" {
		t.Fatalf("T1 state=%s, want waiting (not on the cycle)", s)
	}
	if net.Pending() != 0 {
		t.Fatalf("pending=%d, want 0 (no unbounded forwarding)", net.Pending())
	}
	if m.HasCycle() {
		t.Fatal("wait cycle still exists after all deliveries")
	}
	t.Logf("log:\n%s", buf.String())
}

// 各类非法操作被整体拒绝且原因可区分，锁表与等待关系不变。
func TestRejectedOperations(t *testing.T) {
	var buf bytes.Buffer
	m, _ := newTestManager(&buf)
	m.AddSite("S1")
	must(t, m.AddLock("S1", "LA"))
	must(t, m.AddLock("S1", "LB"))
	must(t, m.Begin("T1", 1))
	must(t, m.Begin("T2", 2))
	grant(t, m, "T1", "S1", "LA", Exclusive)
	queue(t, m, "T2", "S1", "LA", Exclusive)

	cases := []struct {
		name string
		op   func() error
		code ErrCode
	}{
		{"unknown-site", func() error { _, e := m.Request("T1", "SX", "LA", Shared); return e }, ErrUnknownSite},
		{"unknown-lock", func() error { _, e := m.Request("T1", "S1", "LX", Shared); return e }, ErrUnknownLock},
		{"unknown-txn", func() error { _, e := m.Request("TX", "S1", "LA", Shared); return e }, ErrUnknownTxn},
		{"waiting-txn", func() error { _, e := m.Request("T2", "S1", "LB", Shared); return e }, ErrTxnWaiting},
		{"dup-txn", func() error { return m.Begin("T1", 100) }, ErrDuplicateTxn},
		{"dup-ts", func() error { return m.Begin("T3", 2) }, ErrDuplicateStartTS},
		{"release-unheld", func() error { return m.Release("T1", "S1", "LB") }, ErrLockNotHeld},
		{"add-lock-unknown-site", func() error { return m.AddLock("SX", "L") }, ErrUnknownSite},
	}
	for _, c := range cases {
		err := c.op()
		ce, ok := err.(*Error)
		if !ok {
			t.Fatalf("%s: err=%v, want *Error", c.name, err)
		}
		if ce.Code != c.code {
			t.Fatalf("%s: code=%v, want %v", c.name, ce.Code, c.code)
		}
	}

	must(t, m.End("T1"))
	if _, err := m.Request("T1", "S1", "LB", Shared); err == nil {
		t.Fatal("request from ended txn must be rejected")
	} else if ce := err.(*Error); ce.Code != ErrTxnFinished {
		t.Fatalf("ended txn code=%v, want %v", ce.Code, ErrTxnFinished)
	}

	// 拒绝未改变锁表与等待关系：T2 仍等待，T1 结束后被按序授予。
	if s := m.TxnState("T2"); s != "active" {
		t.Fatalf("T2 state=%s, want active (granted after T1 ended)", s)
	}
	if got := m.WaitsFor("T2"); len(got) != 0 {
		t.Fatalf("T2 waits=%v, want none", got)
	}
	if m.HasCycle() {
		t.Fatal("unexpected cycle")
	}
	t.Logf("log:\n%s", buf.String())
}

// 相同的操作与投递序列重放得到相同的牺牲者序列与日志。
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]string, string) {
		var buf bytes.Buffer
		m, net := newTestManager(&buf)
		setupRing(t, m)
		for net.Pending() > 0 {
			net.Deliver(net.Pending() - 1)
		}
		return m.Victims(), buf.String()
	}
	v1, log1 := run()
	v2, log2 := run()
	if !reflect.DeepEqual(v1, v2) {
		t.Fatalf("victims differ across replays: %v vs %v", v1, v2)
	}
	if log1 != log2 {
		t.Fatalf("logs differ across replays:\n%s\n---\n%s", log1, log2)
	}
}

// 请求、释放、结束与消息投递并发调用：注入随机延迟的异步网络，
// 竞态检测下无数据竞争，全部结束后不存在等待环。
func TestConcurrentOperations(t *testing.T) {
	var buf bytes.Buffer
	m := NewManager(log.New(&buf, "", 0))
	rng := rand.New(rand.NewSource(42))
	var rngMu sync.Mutex
	net := NewAsyncNetwork(m.Deliver, func() time.Duration {
		rngMu.Lock()
		defer rngMu.Unlock()
		return time.Duration(rng.Intn(200)) * time.Microsecond
	})
	m.SetNetwork(net)

	const sites = 3
	const txns = 12
	for s := 0; s < sites; s++ {
		site := fmt.Sprintf("S%d", s)
		m.AddSite(site)
		for l := 0; l < 2; l++ {
			must(t, m.AddLock(site, fmt.Sprintf("L%d", l)))
		}
	}
	for i := 0; i < txns; i++ {
		must(t, m.Begin(fmt.Sprintf("T%d", i), int64(i+1)))
	}

	var wg sync.WaitGroup
	for i := 0; i < txns; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			txn := fmt.Sprintf("T%d", i)
			for j := 0; j < 4; j++ {
				site := fmt.Sprintf("S%d", (i+j)%sites)
				lock := fmt.Sprintf("L%d", (i+j)%2)
				mode := Shared
				if (i+j)%3 == 0 {
					mode = Exclusive
				}
				ok, err := m.Request(txn, site, lock, mode)
				if err != nil || !ok {
					break // 被拒绝或进入等待，停止继续请求
				}
			}
			_ = m.End(txn)
		}(i)
	}
	wg.Wait()
	net.Wait()

	if m.HasCycle() {
		t.Fatal("wait cycle exists after all deliveries")
	}
	for i := 0; i < txns; i++ {
		if s := m.TxnState(fmt.Sprintf("T%d", i)); s == "waiting" {
			t.Fatalf("T%d still waiting after all deliveries", i)
		}
	}
	t.Logf("victims=%v", m.Victims())
}
