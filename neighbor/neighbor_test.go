package neighbor

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type testRig struct {
	cache    *Cache
	log      *recordLogger
	deliver  []string
	requests []string
	mu       sync.Mutex
}

type recordLogger struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (r *recordLogger) Logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.WriteString(fmt.Sprintf(format, args...))
	r.buf.WriteByte('\n')
}

func (r *recordLogger) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func newRig(cfg Config) *testRig {
	rig := &testRig{log: &recordLogger{}}
	rig.cache = NewCache(
		cfg,
		func(packet any, ll string) {
			rig.mu.Lock()
			rig.deliver = append(rig.deliver, fmt.Sprintf("%v->%s", packet, ll))
			rig.mu.Unlock()
			rig.log.Logf("OUTPUT deliver packet=%v linklayer=%s", packet, ll)
		},
		func(address string) {
			rig.mu.Lock()
			rig.requests = append(rig.requests, address)
			rig.mu.Unlock()
			rig.log.Logf("OUTPUT request address=%s", address)
		},
		rig.log,
	)
	return rig
}

func testConfig() Config {
	return Config{
		ReachableTime: 10 * time.Second,
		DelayTime:     3 * time.Second,
		RetransTimer:  2 * time.Second,
		MaxAttempts:   3,
		QueueLimit:    2,
		MaxEntries:    10,
	}
}

func at(sec int) time.Time { return time.Unix(int64(sec), 0) }

func (r *testRig) view(t *testing.T, address string) EntryView {
	t.Helper()
	for _, v := range r.cache.Snapshot() {
		if v.Address == address {
			return v
		}
	}
	t.Fatalf("entry %s not found", address)
	return EntryView{}
}

// 未完成时收到对本方请求的回应：记录地址、按序放出暂存包并转可达。
func TestIncompleteSolicitedReachable(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	if err := c.Send(at(0), "A", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(at(0), "A", "p2"); err != nil {
		t.Fatal(err)
	}
	if got := rig.view(t, "A"); got.State != Incomplete || got.Queued != 2 {
		t.Fatalf("want INCOMPLETE queued=2, got %s queued=%d", got.State, got.Queued)
	}
	if err := c.Advertise(at(1), "A", "llA", true, true); err != nil {
		t.Fatal(err)
	}
	got := rig.view(t, "A")
	if got.State != Reachable || got.LinkLayer != "llA" || got.Queued != 0 {
		t.Fatalf("want REACHABLE llA queued=0, got %s %q queued=%d", got.State, got.LinkLayer, got.Queued)
	}
}

// 未完成时收到非回应：记录地址、放出暂存包但转陈旧。
func TestIncompleteUnsolicitedStale(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	if err := c.Send(at(0), "A", "p1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Advertise(at(1), "A", "llA", false, true); err != nil {
		t.Fatal(err)
	}
	got := rig.view(t, "A")
	if got.State != Stale || got.LinkLayer != "llA" || got.Queued != 0 {
		t.Fatalf("want STALE llA queued=0, got %s %q queued=%d", got.State, got.LinkLayer, got.Queued)
	}
	if !got.Deadline.IsZero() {
		t.Fatalf("stale entry must have no deadline, got %v", got.Deadline)
	}
}

// 暂存包按入队顺序且恰好放出一次。
func TestQueueFifoAndOnce(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p1")
	_ = c.Send(at(0), "A", "p2")
	_ = c.Advertise(at(1), "A", "llA", true, true)
	want := []string{"p1->llA", "p2->llA"}
	if len(rig.deliver) != len(want) {
		t.Fatalf("deliveries=%v want %v", rig.deliver, want)
	}
	for i := range want {
		if rig.deliver[i] != want[i] {
			t.Fatalf("deliveries=%v want %v", rig.deliver, want)
		}
	}
}

// 可达到期：now 早于到期保持可达，now 恰在到期边界转陈旧。
func TestReachableExpiryBoundary(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p0")
	_ = c.Advertise(at(0), "A", "llA", true, true)
	if err := c.Tick(at(9)); err != nil {
		t.Fatal(err)
	}
	if got := rig.view(t, "A"); got.State != Reachable {
		t.Fatalf("at t=9 want REACHABLE, got %s", got.State)
	}
	if err := c.Tick(at(10)); err != nil {
		t.Fatal(err)
	}
	got := rig.view(t, "A")
	if got.State != Stale || !got.Deadline.IsZero() {
		t.Fatalf("at boundary t=10 want STALE without deadline, got %s deadline=%v", got.State, got.Deadline)
	}
}

// 陈旧发包转延迟，延迟到期转探测并发第 1 次探测。
func TestStaleToDelayToProbe(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p0")
	_ = c.Advertise(at(0), "A", "llA", true, true)
	_ = c.Tick(at(10))
	before := len(rig.deliver)
	if err := c.Send(at(11), "A", "p1"); err != nil {
		t.Fatal(err)
	}
	got := rig.view(t, "A")
	if got.State != Delay || !got.Deadline.Equal(at(14)) {
		t.Fatalf("want DELAY deadline t=14, got %s deadline=%v", got.State, got.Deadline)
	}
	if len(rig.deliver) != before+1 {
		t.Fatalf("stale send must deliver immediately")
	}
	if err := c.Tick(at(14)); err != nil {
		t.Fatal(err)
	}
	got = rig.view(t, "A")
	if got.State != Probe || got.Sent != 1 || !got.Deadline.Equal(at(16)) {
		t.Fatalf("want PROBE sent=1 deadline t=16, got %s sent=%d deadline=%v", got.State, got.Sent, got.Deadline)
	}
}

// 未完成条目耗尽 K 次发送后删除，全部暂存包计为不可达丢弃。
func TestIncompleteExhaustionDeletesAndDrops(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p1")
	_ = c.Send(at(1), "A", "p2")
	if len(rig.requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(rig.requests))
	}
	_ = c.Tick(at(2))
	got := rig.view(t, "A")
	if got.State != Incomplete || got.Sent != 2 {
		t.Fatalf("want INCOMPLETE sent=2, got %s sent=%d", got.State, got.Sent)
	}
	_ = c.Tick(at(4))
	_ = c.Tick(at(6))
	if len(c.Snapshot()) != 0 {
		t.Fatalf("entry should be deleted, got %+v", c.Snapshot())
	}
	unreachable, overflow := c.Stats()
	if unreachable != 2 || overflow != 0 {
		t.Fatalf("want 2 unreachable drops, got unreachable=%d overflow=%d", unreachable, overflow)
	}
	if len(rig.requests) != 3 {
		t.Fatalf("want exactly 3 requests, got %d (%v)", len(rig.requests), rig.requests)
	}
	if len(rig.deliver) != 0 {
		t.Fatalf("queued packets must never be delivered, got %v", rig.deliver)
	}
}

// 探测态重发 K 次后同样删除。
func TestProbeExhaustionDeletes(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p0")
	_ = c.Advertise(at(0), "A", "llA", true, true)
	_ = c.Tick(at(10))
	_ = c.Send(at(11), "A", "p1")
	_ = c.Tick(at(14))
	_ = c.Tick(at(16))
	_ = c.Tick(at(18))
	_ = c.Tick(at(20))
	if len(c.Snapshot()) != 0 {
		t.Fatalf("probe entry should be deleted, got %+v", c.Snapshot())
	}
}

// 不覆盖且地址不同：可达转陈旧；其余状态忽略且不记录地址。
func TestNoOverrideDifferentLL(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p0")
	_ = c.Advertise(at(0), "A", "llA", true, true)
	if err := c.Advertise(at(1), "A", "llB", false, false); err != nil {
		t.Fatal(err)
	}
	got := rig.view(t, "A")
	if got.State != Stale || got.LinkLayer != "llA" {
		t.Fatalf("reachable: want STALE keep llA, got %s %q", got.State, got.LinkLayer)
	}
	_ = c.Advertise(at(2), "A", "llC", false, false)
	got = rig.view(t, "A")
	if got.State != Stale || got.LinkLayer != "llA" {
		t.Fatalf("stale: ignore expected, got %s %q", got.State, got.LinkLayer)
	}
	_ = c.Send(at(3), "A", "p1")
	_ = c.Advertise(at(3), "A", "llC", false, false)
	got = rig.view(t, "A")
	if got.State != Delay || got.LinkLayer != "llA" {
		t.Fatalf("delay: ignore expected, got %s %q", got.State, got.LinkLayer)
	}
	_ = c.Tick(at(6))
	_ = c.Advertise(at(6), "A", "llC", false, false)
	got = rig.view(t, "A")
	if got.State != Probe || got.LinkLayer != "llA" {
		t.Fatalf("probe: ignore expected, got %s %q", got.State, got.LinkLayer)
	}
}

// 其余状态下覆盖更新地址，回应转可达；非回应同地址状态不变。
func TestOverrideAndSameLL(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p0")
	_ = c.Advertise(at(0), "A", "llA", true, true)
	_ = c.Tick(at(10))
	if err := c.Advertise(at(11), "A", "llB", true, true); err != nil {
		t.Fatal(err)
	}
	got := rig.view(t, "A")
	if got.State != Reachable || got.LinkLayer != "llB" {
		t.Fatalf("override response: want REACHABLE llB, got %s %q", got.State, got.LinkLayer)
	}
	_ = c.Tick(at(21))
	_ = c.Advertise(at(22), "A", "llB", false, false)
	got = rig.view(t, "A")
	if got.State != Stale || got.LinkLayer != "llB" {
		t.Fatalf("same-ll unsolicited: state must stay STALE, got %s %q", got.State, got.LinkLayer)
	}
}

// 上层确认仅使延迟或探测转可达。
func TestConfirmOnlyDelayOrProbe(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p0")
	_ = c.Advertise(at(0), "A", "llA", true, true)
	_ = c.Tick(at(10))
	if err := c.Confirm(at(11), "A"); err != nil {
		t.Fatal(err)
	}
	if got := rig.view(t, "A"); got.State != Stale {
		t.Fatalf("confirm must not affect STALE, got %s", got.State)
	}
	_ = c.Send(at(11), "A", "p1")
	if err := c.Confirm(at(12), "A"); err != nil {
		t.Fatal(err)
	}
	if got := rig.view(t, "A"); got.State != Reachable {
		t.Fatalf("confirm on DELAY want REACHABLE, got %s", got.State)
	}
}

// 暂存溢出：超出 Q 丢最旧并计数。
func TestQueueOverflowDropsOldest(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p1")
	_ = c.Send(at(0), "A", "p2")
	_ = c.Send(at(0), "A", "p3")
	got := rig.view(t, "A")
	if got.Queued != 2 {
		t.Fatalf("want queued=2, got %d", got.Queued)
	}
	unreachable, overflow := c.Stats()
	if unreachable != 0 || overflow != 1 {
		t.Fatalf("want overflow=1, got unreachable=%d overflow=%d", unreachable, overflow)
	}
	_ = c.Advertise(at(1), "A", "llA", true, true)
	want := []string{"p2->llA", "p3->llA"}
	if len(rig.deliver) != len(want) {
		t.Fatalf("deliveries=%v want %v", rig.deliver, want)
	}
	for i := range want {
		if rig.deliver[i] != want[i] {
			t.Fatalf("deliveries=%v want %v", rig.deliver, want)
		}
	}
}

// 可达/延迟/探测发包直接放出。
func TestDirectDeliverStates(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p0")
	_ = c.Advertise(at(0), "A", "llA", true, true)
	_ = c.Send(at(1), "A", "r")
	_ = c.Tick(at(10))
	_ = c.Send(at(10), "A", "s")
	_ = c.Tick(at(13))
	_ = c.Send(at(13), "A", "d")
	_ = c.Send(at(13), "A", "pr")
	for _, want := range []string{"p0->llA", "r->llA", "s->llA", "d->llA", "pr->llA"} {
		found := false
		for _, got := range rig.deliver {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing delivery %s in %v", want, rig.deliver)
		}
	}
}

// 拒绝按固定顺序只报第一个，且被拒绝操作不改变条目与计数（到期处理除外）。
func TestRejectionsOrderAndNoSideEffect(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(10), "A", "p1")

	if err := c.Send(at(5), "", nil); err != ErrClockBackward {
		t.Fatalf("want ErrClockBackward, got %v", err)
	}
	if err := c.Advertise(at(11), "", "", false, false); err != ErrEmptyAddress {
		t.Fatalf("want ErrEmptyAddress, got %v", err)
	}
	if err := c.Advertise(at(11), "missing", "", false, false); err != ErrEmptyLinkLayer {
		t.Fatalf("want ErrEmptyLinkLayer, got %v", err)
	}
	if err := c.Advertise(at(11), "missing", "llX", false, false); err != ErrNoEntry {
		t.Fatalf("want ErrNoEntry, got %v", err)
	}
	if err := c.Confirm(at(11), "missing"); err != ErrNoEntry {
		t.Fatalf("want ErrNoEntry, got %v", err)
	}
	for i := 0; i < 9; i++ {
		addr := string(rune('B' + i))
		if err := c.Send(at(11), addr, "p"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Send(at(11), "Z", "p"); err != ErrEntryLimit {
		t.Fatalf("want ErrEntryLimit, got %v", err)
	}
	if len(c.Snapshot()) != 10 {
		t.Fatalf("want 10 entries, got %d", len(c.Snapshot()))
	}
	for _, v := range c.Snapshot() {
		if v.Address == "Z" {
			t.Fatalf("rejected entry Z must not exist")
		}
	}
	if err := c.Tick(at(0)); err != ErrClockBackward {
		t.Fatalf("want ErrClockBackward on tick, got %v", err)
	}
}

// 到期处理先于拒绝：被拒绝操作携带的到期仍然生效。
func TestExpiryBeforeRejection(t *testing.T) {
	rig := newRig(testConfig())
	c := rig.cache
	_ = c.Send(at(0), "A", "p0")
	_ = c.Advertise(at(0), "A", "llA", true, true)
	err := c.Advertise(at(10), "missing", "", false, false)
	if err != ErrEmptyLinkLayer {
		t.Fatalf("want rejection, got %v", err)
	}
	if got := rig.view(t, "A"); got.State != Stale {
		t.Fatalf("expiry must be applied before rejection, got %s", got.State)
	}
}

// 并发调用下数据不损坏，暂存不超上限。
func TestConcurrentOperations(t *testing.T) {
	cfg := Config{
		ReachableTime: 100 * time.Millisecond,
		DelayTime:     30 * time.Millisecond,
		RetransTimer:  20 * time.Millisecond,
		MaxAttempts:   5,
		QueueLimit:    4,
		MaxEntries:    64,
	}
	rig := newRig(cfg)
	c := rig.cache
	base := time.Unix(0, 0)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			addr := fmt.Sprintf("addr-%d", g%32)
			for i := 0; i < 200; i++ {
				now := base.Add(time.Duration(i) * time.Millisecond)
				switch i % 4 {
				case 0:
					_ = c.Send(now, addr, i)
				case 1:
					_ = c.Advertise(now, addr, "ll-"+addr, true, true)
				case 2:
					_ = c.Confirm(now, addr)
				case 3:
					_ = c.Tick(now)
				}
			}
		}(g)
	}
	wg.Wait()
	for _, v := range c.Snapshot() {
		if v.Queued > cfg.QueueLimit {
			t.Fatalf("queued %d exceeds limit %d", v.Queued, cfg.QueueLimit)
		}
	}
}

// 相同操作序列重放结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	type op struct {
		kind    string
		t       int
		addr    string
		payload string
		ll      string
		sol     bool
		ovr     bool
	}
	ops := []op{
		{"send", 0, "A", "p1", "", false, false},
		{"send", 1, "B", "q1", "", false, false},
		{"send", 2, "A", "p2", "", false, false},
		{"tick", 2, "", "", "", false, false},
		{"adv", 3, "A", "", "llA", true, true},
		{"send", 4, "B", "q2", "", false, false},
		{"tick", 4, "", "", "", false, false},
		{"adv", 5, "B", "", "llB", false, true},
		{"send", 6, "A", "p3", "", false, false},
		{"tick", 13, "", "", "", false, false},
		{"confirm", 14, "B", "", "", false, false},
	}
	run := func() (string, []EntryView, int, int, []string, []string) {
		rig := newRig(testConfig())
		c := rig.cache
		for _, o := range ops {
			var err error
			switch o.kind {
			case "send":
				err = c.Send(at(o.t), o.addr, o.payload)
			case "adv":
				err = c.Advertise(at(o.t), o.addr, o.ll, o.sol, o.ovr)
			case "confirm":
				err = c.Confirm(at(o.t), o.addr)
			case "tick":
				err = c.Tick(at(o.t))
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}
		unreachable, overflow := c.Stats()
		return rig.log.text(), c.Snapshot(), unreachable, overflow, rig.deliver, rig.requests
	}
	log1, snap1, u1, of1, d1, r1 := run()
	log2, snap2, u2, of2, d2, r2 := run()
	if log1 != log2 {
		t.Fatalf("logs differ on replay")
	}
	if fmt.Sprint(snap1) != fmt.Sprint(snap2) {
		t.Fatalf("snapshots differ: %v vs %v", snap1, snap2)
	}
	if u1 != u2 || of1 != of1 {
		t.Fatalf("stats differ: (%d,%d) vs (%d,%d)", u1, of1, u2, of2)
	}
	if fmt.Sprint(d1) != fmt.Sprint(d2) || fmt.Sprint(r1) != fmt.Sprint(r2) {
		t.Fatalf("outputs differ: deliver %v vs %v, requests %v vs %v", d1, d2, r1, r2)
	}
}
