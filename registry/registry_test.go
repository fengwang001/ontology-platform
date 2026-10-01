package registry

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// captureLogger 收集日志，便于测试中打印输入、输出与判定依据。
type captureLogger struct{ sb strings.Builder }

func (c *captureLogger) Logf(format string, args ...any) {
	fmt.Fprintf(&c.sb, format+"\n", args...)
}

func (c *captureLogger) dump(t *testing.T) {
	t.Helper()
	t.Logf("判定日志:\n%s", c.sb.String())
}

func errCode(err error) ErrorCode {
	if err == nil {
		return 0
	}
	return err.(*CallError).Code
}

// TestKeepaliveBoundary：超时判据为严格大于 1500*K。
func TestKeepaliveBoundary(t *testing.T) {
	lg := &captureLogger{}
	r := New(lg)
	will := &Will{Topic: "t", Payload: "p", DelayMillis: 0}

	if err := r.Connect("c", 2, will, 1000); err != nil {
		t.Fatal(err)
	}
	// now-lastActive == 3000 == 1500*2：不超时，活动被接受。
	if err := r.Active("c", 4000); err != nil {
		t.Fatalf("exactly 1500*K ms must stay online: %v", err)
	}
	// 再推进 3000 仍不超时。
	if err := r.Active("c", 7000); err != nil {
		t.Fatalf("exactly 1500*K ms must stay online: %v", err)
	}
	// 多 1 毫秒：入口处理判定超时并立即发布（D=0）。
	if _, err := r.Advance(10001); err != nil {
		t.Fatal(err)
	}
	got := r.Published()
	if len(got) != 1 || got[0].PublishedAt != 10001 {
		t.Fatalf("want one publish at discover now=10001, got %+v", got)
	}
	lg.dump(t)
}

// TestDiscoverTimeNotTheoretical：断线时刻取发现时刻；D>0 按发现时刻+D 发布。
func TestDiscoverTimeAndDelay(t *testing.T) {
	lg := &captureLogger{}
	r := New(lg)
	if err := r.Connect("c", 1, &Will{Topic: "t", Payload: "p", DelayMillis: 10}, 0); err != nil {
		t.Fatal(err)
	}
	// 理论到期时刻是 1501，但 1999 才被发现：计划时刻应为 2009。
	if _, err := r.Advance(1999); err != nil {
		t.Fatal(err)
	}
	if len(r.Published()) != 0 {
		t.Fatalf("will due at 2009 must not publish at 1999")
	}
	if _, err := r.Advance(2008); err != nil {
		t.Fatal(err)
	}
	if len(r.Published()) != 0 {
		t.Fatalf("will due at 2009 must not publish at 2008")
	}
	if _, err := r.Advance(2009); err != nil {
		t.Fatal(err)
	}
	got := r.Published()
	if len(got) != 1 || got[0].PublishedAt != 2009 {
		t.Fatalf("want publish at 2009, got %+v", got)
	}
	lg.dump(t)
}

// TestDZeroSamePass：D=0 的遗嘱在入口处理的同一轮内（超时判定后）发布。
func TestDZeroSamePass(t *testing.T) {
	r := New(&captureLogger{})
	if err := r.Connect("c", 1, &Will{Topic: "t", Payload: "p", DelayMillis: 0}, 0); err != nil {
		t.Fatal(err)
	}
	// 5000 才发现超时（理论到期 1501）：同一次入口处理内完成“超时判定 + D=0 发布”。
	got, err := r.Advance(5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PublishedAt != 5000 {
		t.Fatalf("D=0 must publish within the same entry pass at 5000, got %+v", got)
	}
	if len(r.Published()) != 1 {
		t.Fatalf("exactly one publish record, got %d", len(r.Published()))
	}
}

// TestTakeoverVoidsOldWillAndCancelsPending：在线接管旧遗嘱作废；等待中的遗嘱被取消。
func TestTakeoverCancels(t *testing.T) {
	lg := &captureLogger{}
	r := New(lg)
	// 1) 在线状态下直接接管：旧连接遗嘱作废，新遗嘱正常断线也作废 -> 无发布。
	if err := r.Connect("c", 0, &Will{Topic: "old", Payload: "o", DelayMillis: 0}, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Connect("c", 0, &Will{Topic: "new", Payload: "n", DelayMillis: 0}, 100); err != nil {
		t.Fatal(err)
	}
	if err := r.Disconnect("c", true, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Advance(10_000); err != nil {
		t.Fatal(err)
	}
	if len(r.Published()) != 0 {
		t.Fatalf("takeover must void old will, normal disconnect voids new, got %+v", r.Published())
	}

	// 2) 异常断线进入等待发布，到期前重连接管 -> 等待中的遗嘱取消。
	if err := r.Connect("c", 0, &Will{Topic: "w", Payload: "p", DelayMillis: 1000}, 20_000); err != nil {
		t.Fatal(err)
	}
	if err := r.Disconnect("c", false, 20_100); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Advance(20_500); err != nil {
		t.Fatal(err)
	}
	if err := r.Connect("c", 0, nil, 20_600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Advance(30_000); err != nil {
		t.Fatal(err)
	}
	if len(r.Published()) != 0 {
		t.Fatalf("pending will must be canceled by reconnect takeover, got %+v", r.Published())
	}
	lg.dump(t)
}

// TestReconnectAtDueTime：重连恰在计划发布时刻时，入口处理先发布，遗嘱已不可取消。
func TestReconnectAtDueTime(t *testing.T) {
	lg := &captureLogger{}
	r := New(lg)
	if err := r.Connect("c", 0, &Will{Topic: "t", Payload: "p", DelayMillis: 100}, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Disconnect("c", false, 0); err != nil {
		t.Fatal(err)
	}
	// 同一时刻 connect：入口处理先把 dueAt=100 的遗嘱发布，接管再无 pending 可取消。
	if err := r.Connect("c", 0, nil, 100); err != nil {
		t.Fatal(err)
	}
	got := r.Published()
	if len(got) != 1 || got[0].PublishedAt != 100 {
		t.Fatalf("will scheduled at 100 must already be published at reconnect time 100, got %+v", got)
	}
	if r.pending["c"] != nil {
		t.Fatalf("published will must no longer be pending")
	}
	lg.dump(t)
}

// TestNormalDisconnectNoPublish：正常断开遗嘱作废。
func TestNormalDisconnectNoPublish(t *testing.T) {
	r := New(&captureLogger{})
	if err := r.Connect("c", 0, &Will{Topic: "t", Payload: "p", DelayMillis: 0}, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Disconnect("c", true, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Advance(1_000_000); err != nil {
		t.Fatal(err)
	}
	if len(r.Published()) != 0 {
		t.Fatalf("normal disconnect must void will, got %+v", r.Published())
	}
}

// TestActiveAfterTimeoutRejectedButEntryLands：刚超时的客户端再活动被拒，但入口处理保留。
func TestActiveAfterTimeoutRejectedButEntryLands(t *testing.T) {
	lg := &captureLogger{}
	r := New(lg)
	if err := r.Connect("c", 1, &Will{Topic: "t", Payload: "p", DelayMillis: 0}, 0); err != nil {
		t.Fatal(err)
	}
	err := r.Active("c", 5000)
	if errCode(err) != ErrClientNotOnline {
		t.Fatalf("want ErrClientNotOnline, got %v", err)
	}
	got := r.Published()
	if len(got) != 1 || got[0].PublishedAt != 5000 {
		t.Fatalf("entry timeout publish at 5000 must be retained, got %+v", got)
	}
	// 操作自身无效果：客户端仍不在线，再断开同样被拒。
	if err := r.Disconnect("c", true, 5000); errCode(err) != ErrClientNotOnline {
		t.Fatalf("want still offline, got %v", err)
	}
	lg.dump(t)
}

// TestEntryOrderAndSorting：先全部保活判定（按 id 升序），再统一按（dueAt, 断线序）发布。
func TestEntryOrderAndSorting(t *testing.T) {
	lg := &captureLogger{}
	r := New(lg)
	// b 在 0 连接，a 在 100 连接；K=1。2000 时按 id 升序 a 先断线（seq 小），
	// 但 a 的 dueAt=2100 晚于 b 的 2000，发布顺序应为 b 先 a 后。
	if err := r.Connect("b", 1, &Will{Topic: "tb", Payload: "pb", DelayMillis: 0}, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Connect("a", 1, &Will{Topic: "ta", Payload: "pa", DelayMillis: 100}, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Advance(2000); err != nil {
		t.Fatal(err)
	}
	got := r.Published()
	if len(got) != 1 || got[0].Client != "b" {
		t.Fatalf("b (dueAt=2000) publishes at 2000, a (dueAt=2100) waits, got %+v", got)
	}
	if _, err := r.Advance(2100); err != nil {
		t.Fatal(err)
	}
	got = r.Published()
	if len(got) != 2 || got[1].Client != "a" || got[1].PublishedAt != 2100 {
		t.Fatalf("a publishes at 2100, got %+v", got)
	}

	// 相同 dueAt 时按断线先后序（保活判定按 id 升序：a,m,z）。
	for i, id := range []string{"z", "m", "a"} {
		now := int64(3000 + i*10)
		if err := r.Connect(id, 1, &Will{Topic: "x", Payload: id, DelayMillis: 0}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Advance(10_000); err != nil {
		t.Fatal(err)
	}
	order := r.Published()
	if len(order) != 5 || order[2].Client != "a" || order[3].Client != "m" || order[4].Client != "z" {
		t.Fatalf("same dueAt must publish by disconnect order (a,m,z), got %+v", order)
	}
	lg.dump(t)
}

// TestRejectionPriorityAndAtomicity：时钟倒退与参数非法拒绝不改变任何状态。
func TestRejectionPriorityAndAtomicity(t *testing.T) {
	r := New(&captureLogger{})
	if err := r.Connect("c", 1, &Will{Topic: "t", Payload: "p", DelayMillis: 0}, 1000); err != nil {
		t.Fatal(err)
	}
	// 900 < 1000：即使参数也非法，时钟倒退优先级最高。
	err := r.Connect("", -1, &Will{DelayMillis: -1}, 900)
	if errCode(err) != ErrClockWentBack {
		t.Fatalf("want ErrClockWentBack (highest priority), got %v", err)
	}
	if len(r.Published()) != 0 || len(r.conns) != 1 || len(r.pending) != 0 {
		t.Fatalf("clock-back rejection must change no state, conns=%d pending=%d pub=%d",
			len(r.conns), len(r.pending), len(r.Published()))
	}
	// 1000 不会触发超时（严格大于才超时），参数非法拒绝同样不改状态。
	if err := r.Connect("", 0, nil, 1000); errCode(err) != ErrEmptyClientID {
		t.Fatalf("want ErrEmptyClientID, got %v", err)
	}
	if err := r.Connect("x", -1, nil, 1000); errCode(err) != ErrNegativeKeepAlive {
		t.Fatalf("want ErrNegativeKeepAlive, got %v", err)
	}
	if err := r.Connect("x", 0, &Will{DelayMillis: -1}, 1000); errCode(err) != ErrNegativeDelay {
		t.Fatalf("want ErrNegativeDelay, got %v", err)
	}
	if err := r.Active("", 1000); errCode(err) != ErrEmptyClientID {
		t.Fatalf("want ErrEmptyClientID for Active, got %v", err)
	}
	if len(r.conns) != 1 || len(r.pending) != 0 {
		t.Fatalf("illegal-parameter rejection must change no state")
	}
}

// TestNotOnlineChecksAfterEntry：不在线拒绝发生在入口处理之后，入口的发布予以保留。
func TestNotOnlineChecksAfterEntry(t *testing.T) {
	lg := &captureLogger{}
	r := New(lg)
	if err := r.Connect("keeper", 1, &Will{Topic: "t", Payload: "p", DelayMillis: 0}, 0); err != nil {
		t.Fatal(err)
	}
	err := r.Active("ghost", 5000)
	if errCode(err) != ErrClientNotOnline {
		t.Fatalf("want ErrClientNotOnline for unknown client, got %v", err)
	}
	got := r.Published()
	if len(got) != 1 || got[0].Client != "keeper" || got[0].PublishedAt != 5000 {
		t.Fatalf("entry processing must publish keeper's will before rejecting, got %+v", got)
	}
	err = r.Disconnect("ghost", false, 5000)
	if errCode(err) != ErrClientNotOnline {
		t.Fatalf("want ErrClientNotOnline for disconnect unknown client, got %v", err)
	}
	lg.dump(t)
}

// TestAtMostOneEach：每客户端至多一个在线连接、至多一个等待发布的遗嘱。
func TestAtMostOneEach(t *testing.T) {
	r := New(&captureLogger{})
	_ = r.Connect("c", 0, &Will{Topic: "w", Payload: "p", DelayMillis: 100}, 0)
	_ = r.Disconnect("c", false, 1)
	if r.conns["c"] != nil || r.pending["c"] == nil {
		t.Fatalf("after abnormal disconnect: offline and exactly one pending will")
	}
	_ = r.Connect("c", 0, &Will{Topic: "w2", Payload: "p2", DelayMillis: 100}, 2)
	if r.conns["c"] == nil || r.pending["c"] != nil {
		t.Fatalf("after reconnect: online and old pending canceled")
	}
}

// TestConcurrentCalls：高并发下结果等价于某个串行顺序（race 检测 + 不变量 + 无重复发布）。
func TestConcurrentCalls(t *testing.T) {
	r := New(DiscardLogger{})
	var wg sync.WaitGroup
	const clients = 8
	const rounds = 40
	for w := 0; w < clients; w++ {
		id := fmt.Sprintf("c%d", w)
		wg.Add(1)
		go func() {
			defer wg.Done()
			now := int64(0)
			for k := 0; k < rounds; k++ {
				now += 37
				switch k % 4 {
				case 0:
					_ = r.Connect(id, 2, &Will{Topic: "t", Payload: id, DelayMillis: int64(k % 5)}, now)
				case 1:
					_ = r.Active(id, now)
				case 2:
					_ = r.Disconnect(id, k%5 == 0, now)
				case 3:
					_, _ = r.Advance(now)
				}
			}
		}()
	}
	wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]int{}
	for _, p := range r.published {
		seen[p.Client]++
	}
	if len(r.conns) > clients {
		t.Fatalf("invariant: at most one connection per client, got %d", len(r.conns))
	}
	for _, n := range seen {
		if n > rounds {
			t.Fatalf("will published more times than possible for a client: %d", n)
		}
	}
}
