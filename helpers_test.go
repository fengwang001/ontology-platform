package ontology

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock 是测试可自由拨动的时钟。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// scriptedUpstream 按调用次序返回预置应答；未预置时返回失败。
// started 在每次上游调用被接住时关闭（用于并发栅栏）。
type scriptedUpstream struct {
	mu      sync.Mutex
	answers []upstreamCall
	calls   int
	log     []upstreamCall
	started chan struct{}
	gateMu  sync.Mutex
	gate    chan struct{}
	once    sync.Once
	hanging bool
}

type upstreamCall struct {
	name      string
	rrtype    uint16
	family    AddrFamily
	client    Addr
	srcPrefix int
	answer    Answer
	err       error
}

func newScriptedUpstream(answers []Answer) *scriptedUpstream {
	calls := make([]upstreamCall, len(answers))
	for i, a := range answers {
		calls[i] = upstreamCall{answer: a}
	}
	return &scriptedUpstream{
		answers: calls,
		started: make(chan struct{}, 64),
	}
}

// newGatedUpstream 创建上游：调用停在栅栏处，直到调用 release()。
func newGatedUpstream(answers []Answer) *scriptedUpstream {
	u := newScriptedUpstream(answers)
	u.hanging = true
	u.gate = make(chan struct{})
	return u
}

func (u *scriptedUpstream) Resolve(q Query) (Answer, error) {
	u.mu.Lock()
	idx := u.calls
	u.calls++
	rec := upstreamCall{
		name: q.Name, rrtype: q.Rrtype, family: q.Family,
		client: append(Addr(nil), q.Client...), srcPrefix: q.SrcPrefix,
	}
	u.log = append(u.log, rec)
	var ans Answer
	var err error
	if idx < len(u.answers) {
		ans, err = u.answers[idx].answer, u.answers[idx].err
	} else {
		err = fmt.Errorf("unexpected upstream call")
	}
	u.mu.Unlock()

	select {
	case u.started <- struct{}{}:
	default:
	}
	if u.hanging {
		<-u.gate
	}
	return ans, err
}

// releaseAll 幂等打开门闩；打开后后续上游调用直接通过。
func (u *scriptedUpstream) releaseAll() {
	u.once.Do(func() { close(u.gate) })
}

func v4(a, b, c, d byte) Addr { return Addr{a, b, c, d} }
func v6h(b ...byte) Addr {
	out := make(Addr, 16)
	copy(out, b)
	return out
}

func rec(ttl, scope int) Answer {
	return Answer{Kind: KindRecords, Records: []byte(fmt.Sprintf("R/%d/%d", ttl, scope)), TTL: ttl, ScopePrefix: scope}
}

func mustQuery(t *testing.T, c *Cache, q Query) Result {
	t.Helper()
	r, err := c.Query(q)
	if err != nil {
		t.Fatalf("unexpected error %v for %+v", err, q)
	}
	return r
}

func wantErr(t *testing.T, c *Cache, q Query, want error) {
	t.Helper()
	if _, err := c.Query(q); err != want {
		t.Fatalf("got err %v, want %v for %+v", err, want, q)
	}
}

func waitStarted(t *testing.T, ch <-chan struct{}, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %d upstream start(s)", n)
		}
	}
}

func assertNoMore(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("unexpected extra upstream call")
	case <-time.After(20 * time.Millisecond):
	}
}

// waitFlightWaiters 轮询在途合并组，直到等待者数（含发起者）达到 n。
func waitFlightWaiters(t *testing.T, c *Cache, fk prefixKey, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		got := 0
		if f := c.inFlight[fk]; f != nil {
			got = len(f.waiters) + 1
		}
		c.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d flight waiters", n)
}
