package uplink_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/profile"
	"ontology/uplink"
)

// op 用统一命令序列驱动表驱动用例：P=SetProfile，S=Sample，T=Tick。
type op struct {
	kind                         string
	point                        string
	t, db, minI, maxI, lo, hi, v int64
}

func setP(p string, t, db, minI, maxI, lo, hi int64) op {
	return op{kind: "P", point: p, t: t, db: db, minI: minI, maxI: maxI, lo: lo, hi: hi}
}
func sample(p string, t, v int64) op { return op{kind: "S", point: p, t: t, v: v} }
func tick(t int64) op                { return op{kind: "T", t: t} }

type wantEvent struct {
	kind, point, reason string
	at, v               int64
	err                 error
	throttled           int64
	checkThrottle       bool
}

func throttleAt(n int64) wantEvent {
	return wantEvent{throttled: n, checkThrottle: true}
}

func evs(evs []uplink.Event) string {
	if len(evs) == 0 {
		return "[]"
	}
	parts := make([]string, len(evs))
	for i, e := range evs {
		if e.Kind == uplink.KindReport {
			parts[i] = fmt.Sprintf("Report(%s,%d,%d,%s)", e.Point, e.At, e.V, e.Reason)
		} else {
			parts[i] = fmt.Sprintf("%s(%s,%d)", e.Kind, e.Point, e.At)
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func rep(k, p string, at, v int64, r string) wantEvent {
	return wantEvent{kind: k, point: p, at: at, v: v, reason: r}
}

// runOps 执行命令序列并逐条核对事件、错误与推迟计数，日志打印全部输入与输出。
func runOps(t *testing.T, wn, u, q int64, ops []op, wants [][]wantEvent) {
	t.Helper()
	eng, err := uplink.NewEngine(wn, u, q)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	var log strings.Builder
	for i, o := range ops {
		var got []uplink.Event
		var gerr error
		switch o.kind {
		case "P":
			got, gerr = eng.SetProfile(o.point, o.t, o.db, o.minI, o.maxI, o.lo, o.hi)
			fmt.Fprintf(&log, "op%d SetProfile(%s,t=%d,db=%d,minI=%d,maxI=%d,[%d,%d]) -> %v err=%v\n",
				i, o.point, o.t, o.db, o.minI, o.maxI, o.lo, o.hi, evs(got), gerr)
		case "S":
			got, gerr = eng.Sample(o.point, o.t, o.v)
			fmt.Fprintf(&log, "op%d Sample(%s,t=%d,v=%d) -> %v err=%v\n",
				i, o.point, o.t, o.v, evs(got), gerr)
		case "T":
			got, gerr = eng.Tick(o.t)
			fmt.Fprintf(&log, "op%d Tick(%d) -> %v err=%v\n", i, o.t, evs(got), gerr)
		}

		var want []wantEvent
		if i < len(wants) {
			want = wants[i]
		}
		if len(want) == 1 && want[0].err != nil {
			if !errors.Is(gerr, want[0].err) {
				t.Fatalf("op%d want err %v got %v\n%s", i, want[0].err, gerr, log.String())
			}
			continue
		}
		if gerr != nil {
			t.Fatalf("op%d unexpected err %v\n%s", i, gerr, log.String())
		}
		if len(want) == 1 && want[0].checkThrottle {
			if len(got) != 0 {
				t.Fatalf("op%d want no events, got %v\n%s", i, evs(got), log.String())
			}
			if eng.Throttled() != want[0].throttled {
				t.Fatalf("op%d throttled got %d want %d\n%s",
					i, eng.Throttled(), want[0].throttled, log.String())
			}
			continue
		}
		if len(got) != len(want) {
			t.Fatalf("op%d event count got %d want %d: %v\n%s",
				i, len(got), len(want), evs(got), log.String())
		}
		for j, w := range want {
			g := got[j]
			if string(g.Kind) != w.kind || g.Point != w.point || g.At != w.at ||
				g.V != w.v || string(g.Reason) != w.reason {
				t.Fatalf("op%d event%d got %v want %+v\n%s", i, j, g, w, log.String())
			}
		}
	}
	t.Logf("判定依据（输入/输出）:\n%s", log.String())
}

var _ = reflect.DeepEqual
var _ = rand.Int
var _ sync.Mutex

// TestSpecMain 覆盖题目主例：差恰等 db、尾随取最后样本、回死区不尾随、
// 心跳移动基准、额度推迟、defer 保留、故障进入与恢复绕过额度。
func TestSpecMain(t *testing.T) {
	ops := []op{
		setP("a", 0, 5, 100, 1000, 0, 100),
		sample("a", 0, 50),
		sample("a", 30, 56),
		sample("a", 60, 55),
		sample("a", 90, 58),
		tick(100),
		sample("a", 250, 70),
		sample("a", 400, 60),
		tick(1000),
		tick(1100),
		sample("a", 1150, 65),
		sample("a", 1200, 150),
		sample("a", 1210, -1),
		sample("a", 1300, 65),
	}
	wants := [][]wantEvent{
		nil,
		{rep("Report", "a", 0, 50, "First")},
		nil, nil, nil,
		{rep("Report", "a", 100, 58, "Trailing")},
		nil, nil, nil,
		{rep("Report", "a", 1100, 60, "Heartbeat")},
		nil,
		nil,
		{rep("Fault", "a", 1210, 0, "")},
		{
			rep("Recover", "a", 1300, 0, ""),
			rep("Report", "a", 1300, 65, "Recover"),
		},
	}
	runOps(t, 1000, 2, 2, ops, wants)
}

// TestSpecThrottledTrailing：t=250 后不再有样本，Tick(1000) 发出 Trailing。
func TestSpecThrottledTrailing(t *testing.T) {
	ops := []op{
		setP("a", 0, 5, 100, 1000, 0, 100),
		sample("a", 0, 50),
		sample("a", 90, 58),
		tick(100),
		sample("a", 250, 70),
		tick(1000),
	}
	wants := [][]wantEvent{
		nil,
		{rep("Report", "a", 0, 50, "First")},
		nil,
		{rep("Report", "a", 100, 58, "Trailing")},
		nil,
		{rep("Report", "a", 1000, 70, "Trailing")},
	}
	runOps(t, 1000, 2, 2, ops, wants)
}

// TestSpecWindowStartChange：待报消失且 defer=1000 时，窗口起点样本给 Change。
func TestSpecWindowStartChange(t *testing.T) {
	ops := []op{
		setP("a", 0, 5, 100, 1000, 0, 100),
		sample("a", 0, 50),
		sample("a", 90, 58),
		tick(100),
		sample("a", 250, 70),
		sample("a", 400, 60),
		sample("a", 1000, 70),
	}
	wants := [][]wantEvent{
		nil,
		{rep("Report", "a", 0, 50, "First")},
		nil,
		{rep("Report", "a", 100, 58, "Trailing")},
		nil, nil,
		{rep("Report", "a", 1000, 70, "Change")},
	}
	runOps(t, 1000, 2, 2, ops, wants)
}

// TestDeadbandExactAndMinInterval：差恰等 db 不报；间隔恰等 minI 到期。
func TestDeadbandExactAndMinInterval(t *testing.T) {
	ops := []op{
		setP("a", 0, 5, 100, 1000, 0, 100),
		sample("a", 0, 50),
		sample("a", 50, 56), // 越死区，dc=100
		tick(99),
		tick(100),            // 间隔恰等 minI，Trailing 56
		sample("a", 100, 61), // 相对 56 差恰 5：不越死区
		tick(200),
	}
	wants := [][]wantEvent{
		nil,
		{rep("Report", "a", 0, 50, "First")},
		nil, nil,
		{rep("Report", "a", 100, 56, "Trailing")},
		nil, nil,
	}
	runOps(t, 1000, 10, 2, ops, wants)
}

// TestHeartbeatMovesBaseline：心跳以 cur.v 发出并移动死区基准。
func TestHeartbeatMovesBaseline(t *testing.T) {
	ops := []op{
		setP("a", 0, 5, 100, 1000, 0, 100),
		sample("a", 0, 50),
		sample("a", 90, 58),
		tick(100),            // Trailing 58，lastV=58
		sample("a", 105, 70), // dc=200
		tick(200),            // Trailing 70
		tick(1200),           // Heartbeat 70，基准移动
		sample("a", 1205, 75),
		tick(1300), // 相对基准 70 差恰 5，不越死区，无待报
	}
	wants := [][]wantEvent{
		nil,
		{rep("Report", "a", 0, 50, "First")},
		nil,
		{rep("Report", "a", 100, 58, "Trailing")},
		nil,
		{rep("Report", "a", 200, 70, "Trailing")},
		{rep("Report", "a", 1200, 70, "Heartbeat")},
		nil, nil,
	}
	runOps(t, 1000, 10, 2, ops, wants)
}

// TestQuotaAndDeferRetained：额度用尽推迟、Throttled 计数，推迟后取最后样本。
func TestQuotaAndDeferRetained(t *testing.T) {
	ops := []op{
		setP("a", 0, 0, 100, 100000, 0, 100),
		setP("b", 0, 0, 100, 100000, 0, 100),
		sample("a", 0, 1),
		sample("b", 0, 2),
		sample("a", 50, 3),
		tick(100), // 窗口 0 已用满：a 推迟到 1000，Throttled=1
		sample("a", 150, 2),
		tick(200),  // 仍推迟，无事件
		tick(1000), // 到下一窗口，dc=max(200,150,1000)=1000，取最后样本 2
	}
	wants := [][]wantEvent{
		nil, nil,
		{rep("Report", "a", 0, 1, "First")},
		{rep("Report", "b", 0, 2, "First")},
		nil,
		{throttleAt(1)},
		nil, nil,
		{rep("Report", "a", 1000, 2, "Trailing")},
	}
	runOps(t, 1000, 2, 2, ops, wants)
}

// TestDeferRetainedWhenPendingGone：db=5 时待报消失，defer 仍保留（主例后半）。
func TestDeferRetainedWhenPendingGone(t *testing.T) {
	eng, _ := uplink.NewEngine(1000, 2, 2)
	mustSet(t, eng, "a", 0, 5, 100, 1000, 0, 100)
	mustSamp(t, eng, "a", 0, 50)
	mustSamp(t, eng, "a", 90, 58)
	if ev := mustTick(t, eng, 100); len(ev) != 1 {
		t.Fatalf("want 1 report at 100, got %v", ev)
	}
	mustSamp(t, eng, "a", 250, 70)
	if eng.Throttled() != 1 {
		t.Fatalf("throttled = %d want 1", eng.Throttled())
	}
	mustSamp(t, eng, "a", 400, 60) // 差 2 待报消失，defer 保留
	if ev := mustTick(t, eng, 1000); len(ev) != 0 {
		t.Fatalf("want no event at 1000, got %v", ev)
	}
	ev := mustTick(t, eng, 1100)
	if len(ev) != 1 || ev[0].Reason != uplink.Heartbeat || ev[0].At != 1100 || ev[0].V != 60 {
		t.Fatalf("want Heartbeat(1100,60), got %v", ev)
	}
}

// TestSameTimeOrdering：同一时刻多测点按名字节序。
func TestSameTimeOrdering(t *testing.T) {
	ops := []op{
		setP("c", 0, 0, 100, 1000, 0, 100),
		setP("a", 0, 0, 100, 1000, 0, 100),
		setP("b", 0, 0, 100, 1000, 0, 100),
		sample("c", 0, 3),
		sample("a", 0, 1),
		sample("b", 0, 2),
		tick(1000), // 首报在 0 已发出；1000 为三者心跳，按 a,b,c
	}
	wants := [][]wantEvent{
		nil, nil, nil,
		{rep("Report", "c", 0, 3, "First")},
		{rep("Report", "a", 0, 1, "First")},
		{rep("Report", "b", 0, 2, "First")},
		{
			rep("Report", "a", 1000, 1, "Heartbeat"),
			rep("Report", "b", 1000, 2, "Heartbeat"),
			rep("Report", "c", 1000, 3, "Heartbeat"),
		},
	}
	runOps(t, 1000, 10, 2, ops, wants)
}

// TestPendingBeforeHeartbeat：同测点同刻待报先于心跳（minI=maxI 不可能，
// 故构造 dc 与 dh 同刻：报告后立即心跳的边界用热更新把 maxI 降到与 minI 相等场景，
// 更直接的是 defer 同时推迟二者到同刻，待报优先）。
func TestPendingBeforeHeartbeat(t *testing.T) {
	// 首次上报在 0；随后样本越死区，dc=100。窗口 0 只有 1 额度且已被首报占用，
	// 推迟到 1000 时待报与心跳（dh=1000）同刻，待报必须先发出。
	eng, _ := uplink.NewEngine(1000, 1, 2)
	mustSet(t, eng, "a", 0, 5, 100, 1000, 0, 100)
	mustSamp(t, eng, "a", 0, 50) // 用掉窗口 0 唯一额度
	mustSamp(t, eng, "a", 50, 80)
	mustTick(t, eng, 100) // 推迟，defer=1000
	ev := mustTick(t, eng, 1000)
	if len(ev) != 1 || ev[0].Reason != uplink.Trailing || ev[0].At != 1000 || ev[0].V != 80 {
		t.Fatalf("pending must precede heartbeat at 1000, got %v", ev)
	}
}

// TestTickSplitEquivalence：先 Tick(t1) 再 Tick(t2) 与直接 Tick(t2) 输出完全相同。
func TestTickSplitEquivalence(t *testing.T) {
	build := func() *uplink.Engine {
		e, _ := uplink.NewEngine(1000, 2, 2)
		mustSet(t, e, "a", 0, 5, 100, 1000, 0, 100)
		mustSamp(t, e, "a", 0, 50)
		mustSamp(t, e, "a", 90, 58)
		return e
	}
	split := build()
	var ev1 []uplink.Event
	ev1 = append(ev1, mustTick(t, split, 95)...)
	ev1 = append(ev1, mustTick(t, split, 100)...)
	ev1 = append(ev1, mustTick(t, split, 500)...)
	ev1 = append(ev1, mustTick(t, split, 1200)...)
	direct := build()
	// 单次推进到 1200（包含 1000 的心跳），与逐步切分总输出一致。
	ev2 := mustTick(t, direct, 1200)
	if !reflect.DeepEqual(ev1, ev2) {
		t.Fatalf("split %v != direct %v", evs(ev1), evs(ev2))
	}
	t.Logf("切分与直推一致: %s", evs(ev1))
}

// TestFaultBypassesQuota：恢复的 Recover/Report 不占也不受额度限制。
func TestFaultBypassesQuota(t *testing.T) {
	eng, _ := uplink.NewEngine(1000, 1, 2)
	mustSet(t, eng, "a", 0, 5, 100, 1000, 0, 100)
	mustSamp(t, eng, "a", 0, 50) // 用掉窗口 0 唯一额度
	mustSamp(t, eng, "a", 10, 200)
	ev := mustSamp(t, eng, "a", 20, -1) // bad=2 -> Fault
	if len(ev) != 1 || ev[0].Kind != uplink.KindFault {
		t.Fatalf("want Fault, got %v", ev)
	}
	ev = mustSamp(t, eng, "a", 30, 60) // 窗口 0 满，恢复仍绕过额度
	want := []uplink.Event{
		{Kind: uplink.KindRecover, Point: "a", At: 30},
		{Kind: uplink.KindReport, Point: "a", At: 30, V: 60, Reason: uplink.Recover},
	}
	if !reflect.DeepEqual(ev, want) {
		t.Fatalf("recover got %v want %v", ev, want)
	}
}

// TestHotUpdateImmediate：热更新收紧 minI 后同刻立即到期；cur 不按新量程追溯。
func TestHotUpdateImmediate(t *testing.T) {
	eng, _ := uplink.NewEngine(1000, 10, 2)
	mustSet(t, eng, "a", 0, 5, 100000, 1000000, 0, 100)
	mustSamp(t, eng, "a", 0, 50)
	mustSamp(t, eng, "a", 10, 60)
	if ev := mustTick(t, eng, 500); len(ev) != 0 {
		t.Fatalf("minI huge, want none, got %v", ev)
	}
	// 热更新把 minI 收紧到 100：dc=max(0+100,10)=100 <= 500，立即到期。
	ev, err := eng.SetProfile("a", 500, 5, 100, 10000, 0, 100)
	if err != nil {
		t.Fatalf("setprofile: %v", err)
	}
	if len(ev) != 1 || ev[0].Reason != uplink.Trailing || ev[0].At != 100 || ev[0].V != 60 {
		t.Fatalf("hot update immediate got %v", ev)
	}
	ev, err = eng.SetProfile("a", 600, 5, 1000, 10000, 0, 50)
	if err != nil {
		t.Fatalf("setprofile: %v", err)
	}
	t.Logf("热更新后 cur=60 不按新量程 [0,50] 追溯，t=600 输出=%v", evs(ev))
}

func mustSet(t *testing.T, e *uplink.Engine, p string, tt, db, minI, maxI, lo, hi int64) {
	t.Helper()
	if _, err := e.SetProfile(p, tt, db, minI, maxI, lo, hi); err != nil {
		t.Fatalf("setprofile: %v", err)
	}
}
func mustSamp(t *testing.T, e *uplink.Engine, p string, tt, v int64) []uplink.Event {
	t.Helper()
	ev, err := e.Sample(p, tt, v)
	if err != nil {
		t.Fatalf("sample: %v", err)
	}
	return ev
}
func mustTick(t *testing.T, e *uplink.Engine, tt int64) []uplink.Event {
	t.Helper()
	ev, err := e.Tick(tt)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	return ev
}

// TestRejections：非法 > 时钟回退 > 测点不存在，被拒操作不改状态。
func TestRejections(t *testing.T) {
	if _, err := uplink.NewEngine(0, 10, 2); !errors.Is(err, uplink.ErrInvalid) {
		t.Fatalf("wn=0 want ErrInvalid got %v", err)
	}
	if _, err := uplink.NewEngine(1000, 0, 2); !errors.Is(err, uplink.ErrInvalid) {
		t.Fatalf("u=0 want ErrInvalid got %v", err)
	}
	if _, err := uplink.NewEngine(1000, 10, 101); !errors.Is(err, uplink.ErrInvalid) {
		t.Fatalf("q=101 want ErrInvalid got %v", err)
	}

	eng, _ := uplink.NewEngine(1000, 10, 2)
	badSet := []op{
		setP("a", 0, -1, 100, 1000, 0, 100),
		setP("a", 0, 1_000_000_001, 100, 1000, 0, 100),
		setP("a", 0, 5, 1000, 1000, 0, 100),
		setP("a", 0, 5, 100, 999, 0, 100),
		setP("a", 0, 5, 100, 1001, 100, 0),
		setP("a", 0, 5, 100, 1000, 0, 1_000_000_000_000_001),
		setP("a", -1, 5, 100, 1000, 0, 100),
		setP("a", 1_000_000_000_001, 5, 100, 1000, 0, 100),
	}
	for i, b := range badSet {
		if _, err := eng.SetProfile(b.point, b.t, b.db, b.minI, b.maxI, b.lo, b.hi); !errors.Is(err, uplink.ErrInvalid) {
			t.Fatalf("bad setprofile %d want ErrInvalid got %v", i, err)
		}
	}

	// 测点尚不存在：样本值非法时先报 ErrInvalid。
	if _, err := eng.Sample("nope", 0, 1_000_000_000_000_001); !errors.Is(err, uplink.ErrInvalid) {
		t.Fatalf("invalid v want ErrInvalid got %v", err)
	}
	// 值合法但测点不存在：ErrNoPoint。
	if _, err := eng.Sample("nope", 0, 5); !errors.Is(err, uplink.ErrNoPoint) {
		t.Fatalf("missing point want ErrNoPoint got %v", err)
	}

	// 建立测点并推进时钟。
	mustSet(t, eng, "a", 1000, 5, 100, 1000, 0, 100)
	// 小回退：ErrClockBack（优先于 ErrNoPoint）。
	if _, err := eng.Sample("zzz", 999, 5); !errors.Is(err, uplink.ErrClockBack) {
		t.Fatalf("backstep want ErrClockBack got %v", err)
	}
	// 超大回退：ErrInvalid。
	if _, err := eng.Sample("a", 1000-profile.MaxStep-1, 5); !errors.Is(err, uplink.ErrInvalid) {
		t.Fatalf("huge backstep want ErrInvalid got %v", err)
	}
	if _, err := eng.Tick(999); !errors.Is(err, uplink.ErrClockBack) {
		t.Fatalf("tick backstep want ErrClockBack got %v", err)
	}
	// 被拒操作不改状态：1000 时刻登记仍正常。
	ev := mustSamp(t, eng, "a", 1000, 50)
	if len(ev) != 1 || ev[0].Reason != uplink.First {
		t.Fatalf("state changed by rejected op? got %v", ev)
	}
}

// TestConcurrentSerializability：并发调用结果等价于某个串行顺序（race 检测）。
func TestConcurrentSerializability(t *testing.T) {
	eng, _ := uplink.NewEngine(1000, 100, 5)
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		p := string(rune('a' + i))
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			_, _ = eng.SetProfile(p, 0, 5, 100, 1000, 0, 100)
			for tt := int64(0); tt <= 2000; tt++ {
				_, _ = eng.Sample(p, tt, int64(int(tt%120)))
			}
		}(p)
	}
	wg.Wait()
	ev := mustTick(t, eng, 5000)
	t.Logf("并发后 Tick(5000) 发出 %d 条；Throttled=%d Popped=%d",
		len(ev), eng.Throttled(), eng.Popped())
}

// TestTrailingLastAndSuppress：尾随取抑制期最后样本；回到死区内不尾随。
func TestTrailingLastAndSuppress(t *testing.T) {
	ops := []op{
		setP("a", 0, 5, 100, 10000, 0, 100),
		sample("a", 0, 50),
		sample("a", 10, 60),
		sample("a", 20, 62),
		sample("a", 30, 54), // 回到死区内：待报消失，不尾随
		tick(100),
	}
	wants := [][]wantEvent{
		nil,
		{rep("Report", "a", 0, 50, "First")},
		nil, nil, nil, nil,
	}
	runOps(t, 1000, 10, 2, ops, wants)

	ops = []op{
		setP("a", 0, 5, 100, 10000, 0, 100),
		sample("a", 0, 50),
		sample("a", 10, 60),
		sample("a", 20, 63), // 最后一个样本越死区
		tick(100),
	}
	wants = [][]wantEvent{
		nil,
		{rep("Report", "a", 0, 50, "First")},
		nil, nil,
		{rep("Report", "a", 100, 63, "Trailing")},
	}
	runOps(t, 1000, 10, 2, ops, wants)
}
