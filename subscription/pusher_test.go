package subscription

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
)

// testLogger 构造一个同时输出到测试日志与 strings.Builder 的结构化日志器，
// 便于断言“输入、结果与判定依据”确实被打印。
func testLogger(t *testing.T) (*slog.Logger, *strings.Builder) {
	t.Helper()
	var buf strings.Builder
	writer := io.MultiWriter(&buf, testWriter{t})
	logger := slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, &buf
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func seqsOf(notifications []Notification) []int64 {
	out := make([]int64, len(notifications))
	for i, n := range notifications {
		out[i] = n.Seq
	}
	return out
}

func keysOf(notifications []Notification) []string {
	out := make([]string, len(notifications))
	for i, n := range notifications {
		out[i] = n.Key
	}
	return out
}

// TestBoundaryEqualityHits 验证键恰好等于下界时必须命中，小于下界不命中。
func TestBoundaryEqualityHits(t *testing.T) {
	logger, _ := testLogger(t)
	pusher := New(logger)

	if start, err := pusher.Subscribe("s-equal", "k010"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	} else if start != 0 {
		t.Fatalf("fresh pusher start seq = %d, want 0", start)
	}

	below := pusher.Push(Change{Key: "k009", Value: "below"})
	equal := pusher.Push(Change{Key: "k010", Value: "exact-boundary"})
	above := pusher.Push(Change{Key: "k011", Value: "above"})

	if below != 1 || equal != 2 || above != 3 {
		t.Fatalf("seqs not continuous: %d %d %d", below, equal, above)
	}

	got := keysOf(pusher.DeliveredChanges("s-equal"))
	want := []string{"k010", "k011"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("boundary match keys = %v, want %v (Key == lowerBound must hit)", got, want)
	}
	if pusher.Position() != 3 {
		t.Fatalf("position = %d, want 3", pusher.Position())
	}
}

// TestNoBackfillBeforeSubscribe 验证注册前已投递的历史变更即使命中也不补推。
func TestNoBackfillBeforeSubscribe(t *testing.T) {
	logger, _ := testLogger(t)
	pusher := New(logger)

	pusher.Push(Change{Key: "k100", Value: "history-1"})
	pusher.Push(Change{Key: "k200", Value: "history-2"})

	start, err := pusher.Subscribe("late-sub", "k000")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if start != 2 {
		t.Fatalf("start seq = %d, want 2 (no backfill)", start)
	}

	pusher.Push(Change{Key: "k100", Value: "incremental-1"})
	pusher.Push(Change{Key: "k300", Value: "incremental-2"})

	got := keysOf(pusher.DeliveredChanges("late-sub"))
	want := []string{"k100", "k300"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("delivered keys = %v, want only post-registration %v", got, want)
	}
	seqSet := seqsOf(pusher.DeliveredChanges("late-sub"))
	if seqSet[0] != 3 || seqSet[1] != 4 {
		t.Fatalf("delivered seqs = %v, want [3 4]", seqSet)
	}
}

// TestUnsubscribeZeroPush 验证退订后不再收到任何推送，再次推送也无效。
func TestUnsubscribeZeroPush(t *testing.T) {
	logger, _ := testLogger(t)
	pusher := New(logger)

	if _, err := pusher.Subscribe("gone", "k000"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	pusher.Push(Change{Key: "k001", Value: "while-active"})
	if err := pusher.Unsubscribe("gone"); err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	for i := 0; i < 5; i++ {
		pusher.Push(Change{Key: fmt.Sprintf("k%03d", i+2), Value: "after-unsub"})
	}

	if got := pusher.DeliveredChanges("gone"); got != nil {
		t.Fatalf("delivered after unsubscribe = %v, want nil (zero push)", got)
	}
	if snap := pusher.Snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot after unsubscribe = %v, want empty", snap)
	}
}

// TestRejectedInputsKeepState 验证三类非法输入被拒、错误可判定且互不相同，
// 并且失败不改变任何状态，被拒后仍可正常使用。
func TestRejectedInputsKeepState(t *testing.T) {
	logger, logBuf := testLogger(t)
	pusher := New(logger)

	if _, err := pusher.Subscribe("dup", "b000"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	pusher.Push(Change{Key: "b001", Value: "before-rejections"})

	// 1) 非法订阅标识（空串与全空白）。
	if _, err := pusher.Subscribe("", "b000"); !errors.Is(err, ErrInvalidSubscriptionID) {
		t.Fatalf("empty subscribe err = %v, want ErrInvalidSubscriptionID", err)
	}
	if _, err := pusher.Subscribe("   ", "b000"); !errors.Is(err, ErrInvalidSubscriptionID) {
		t.Fatalf("blank subscribe err = %v, want ErrInvalidSubscriptionID", err)
	}
	// 2) 重复订阅同一标识。
	if _, err := pusher.Subscribe("dup", "b999"); !errors.Is(err, ErrDuplicateSubscription) {
		t.Fatalf("duplicate subscribe err = %v, want ErrDuplicateSubscription", err)
	}
	// 3) 退订未注册标识（含从未注册与非法标识）。
	if err := pusher.Unsubscribe("never-existed"); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("unknown unsubscribe err = %v, want ErrSubscriptionNotFound", err)
	}
	if err := pusher.Unsubscribe(""); !errors.Is(err, ErrInvalidSubscriptionID) {
		t.Fatalf("blank unsubscribe err = %v, want ErrInvalidSubscriptionID", err)
	}

	// 三类错误必须互不相同、可判定。
	errSet := []error{ErrInvalidSubscriptionID, ErrDuplicateSubscription, ErrSubscriptionNotFound}
	for i := range errSet {
		for j := i + 1; j < len(errSet); j++ {
			if errors.Is(errSet[i], errSet[j]) || errSet[i].Error() == errSet[j].Error() {
				t.Fatalf("errors %v and %v must be distinct", errSet[i], errSet[j])
			}
		}
	}

	// 状态不变：位点仍为 1，快照仍只有原订阅且下界/位点未被重复注册改写，
	// 原订阅只收到拒绝前的那一条。
	if pusher.Position() != 1 {
		t.Fatalf("position after rejections = %d, want 1", pusher.Position())
	}
	snap := pusher.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot size after rejections = %d, want 1", len(snap))
	}
	if entry := snap["dup"]; entry.LowerBound != "b000" || entry.StartSeq != 0 {
		t.Fatalf("dup state altered: %+v", entry)
	}
	if got := keysOf(pusher.DeliveredChanges("dup")); len(got) != 1 || got[0] != "b001" {
		t.Fatalf("dup delivered altered: %v, want [b001]", got)
	}

	// 被拒后仍可继续正常使用：新订阅、退订、推送均工作。
	if _, err := pusher.Subscribe("after", "b010"); err != nil {
		t.Fatalf("subscribe after rejections failed: %v", err)
	}
	pusher.Push(Change{Key: "b020", Value: "recovery"})
	if got := keysOf(pusher.DeliveredChanges("after")); len(got) != 1 || got[0] != "b020" {
		t.Fatalf("post-rejection usage broken, delivered = %v", got)
	}

	// 日志必须包含输入、结果与判定依据。
	logs := logBuf.String()
	for _, want := range []string{
		"subscribe rejected: invalid id",
		"subscribe rejected: duplicate id",
		"unsubscribe rejected: id not registered",
		`error="subscription: invalid subscription id"`,
		`reason="id already registered"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing evidence %q\n--- logs ---\n%s", want, logs)
		}
	}
}

// TestConcurrentPushPositionAndDelivery 并发推送下：
//   - 最终位点恰好等于推送总数，序号无洞；
//   - 每个活跃订阅收到的集合恰好等于命中它的那些变更。
func TestConcurrentPushPositionAndDelivery(t *testing.T) {
	logger, _ := testLogger(t)
	pusher := New(logger)

	// "all" 下界为空串，命中一切，用于验证序号 1..N 无洞且总数正确。
	if _, err := pusher.Subscribe("all", ""); err != nil {
		t.Fatal(err)
	}
	// "mid" 只应收到键字典序 >= k050 的变更。
	if _, err := pusher.Subscribe("mid", "k050"); err != nil {
		t.Fatal(err)
	}

	const goroutines = 8
	const perGoroutine = 125
	total := goroutines * perGoroutine // 1000 条

	// 每个 goroutine 使用不相交的键区间（等宽零填充，保证字典序与数值序一致），
	// 从而可以精确推导每个订阅的期望命中集合。
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				idx := g*perGoroutine + i
				pusher.Push(Change{
					Key:   fmt.Sprintf("k%04d", idx),
					Value: fmt.Sprintf("g%d-%d", g, i),
				})
			}
		}(g)
	}
	wg.Wait()

	if pos := pusher.Position(); pos != int64(total) {
		t.Fatalf("position = %d, want %d", pos, total)
	}

	allSeqs := seqsOf(pusher.DeliveredChanges("all"))
	if len(allSeqs) != total {
		t.Fatalf("all subscriber got %d notifications, want %d", len(allSeqs), total)
	}
	sort.Slice(allSeqs, func(i, j int) bool { return allSeqs[i] < allSeqs[j] })
	for i, seq := range allSeqs {
		if seq != int64(i+1) {
			t.Fatalf("seq gap at index %d: got %d, want %d", i, seq, i+1)
		}
	}

	mid := pusher.DeliveredChanges("mid")
	wantMid := 0
	for idx := 0; idx < total; idx++ {
		if fmt.Sprintf("k%04d", idx) >= "k050" {
			wantMid++
		}
	}
	if len(mid) != wantMid {
		t.Fatalf("mid subscriber got %d, want %d", len(mid), wantMid)
	}
	for _, n := range mid {
		if n.Key < "k050" {
			t.Fatalf("mid received non-matching key %q (seq %d)", n.Key, n.Seq)
		}
	}

	// 并发读在 race 检测下也必须安全。
	var readerWG sync.WaitGroup
	for i := 0; i < 4; i++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			_ = pusher.Position()
			_ = pusher.Snapshot()
			_ = pusher.DeliveredChanges("all")
			_ = pusher.DeliveredChanges("mid")
		}()
	}
	readerWG.Wait()
}

// TestConcurrentSubscribeUnsubscribeAndPush 并发订阅/退订/推送时不发生竞态，
// 且最终位点等于推送总数，所有存活订阅位点都合法。
func TestConcurrentSubscribeUnsubscribeAndPush(t *testing.T) {
	logger, _ := testLogger(t)
	pusher := New(logger)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			pusher.Push(Change{Key: fmt.Sprintf("k%04d", i), Value: "x"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("sub-%02d", i)
			if _, err := pusher.Subscribe(id, "k000"); err != nil &&
				!errors.Is(err, ErrDuplicateSubscription) {
				t.Errorf("unexpected subscribe error: %v", err)
				return
			}
			if err := pusher.Unsubscribe(id); err != nil &&
				!errors.Is(err, ErrSubscriptionNotFound) {
				t.Errorf("unexpected unsubscribe error: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	if pusher.Position() != 200 {
		t.Fatalf("position = %d, want 200", pusher.Position())
	}
	for id, entry := range pusher.Snapshot() {
		if entry.StartSeq > pusher.Position() {
			t.Fatalf("subscriber %s start %d beyond position %d", id, entry.StartSeq, pusher.Position())
		}
	}
}
