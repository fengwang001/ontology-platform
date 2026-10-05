package throttle

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/classify"
	"ontology/ocstate"
)

func lowMsg() classify.Message {
	return classify.Message{Method: "OPTIONS", Priority: classify.PriorityNormal}
}

func normalMsg() classify.Message {
	return classify.Message{Method: "INVITE", Priority: classify.PriorityNormal}
}

func exemptMsg() classify.Message {
	return classify.Message{Method: "BYE", Priority: classify.PriorityNormal}
}

func repeatMsg(m classify.Message, n int) []classify.Message {
	out := make([]classify.Message, n)
	for i := range out {
		out[i] = m
	}
	return out
}

func alternateMsg(a, b classify.Message, n int) []classify.Message {
	out := make([]classify.Message, n)
	for i := range out {
		if i%2 == 0 {
			out[i] = a
		} else {
			out[i] = b
		}
	}
	return out
}

func mustNew(t *testing.T, thetaLow, thetaNorm, dCap, window int64) *Controller {
	t.Helper()
	c, err := New(thetaLow, thetaNorm, dCap, window)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func mustAdd(t *testing.T, c *Controller, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		if err := c.AddServer(id); err != nil {
			t.Fatalf("AddServer(%d): %v", id, err)
		}
	}
}

func mustReport(t *testing.T, c *Controller, server, seq, percent, validity, now int64) {
	t.Helper()
	if err := c.Report(server, seq, percent, validity, now); err != nil {
		t.Fatalf("Report(server=%d seq=%d): %v", server, seq, err)
	}
}

func mustRoute(t *testing.T, c *Controller, msg classify.Message, cands []int64, now int64) Result {
	t.Helper()
	res, err := c.Route(msg, cands, now)
	if err != nil {
		t.Fatalf("Route(cands=%v now=%d): %v", cands, now, err)
	}
	return res
}

func mustDone(t *testing.T, c *Controller, server, now int64) {
	t.Helper()
	if err := c.Done(server, now); err != nil {
		t.Fatalf("Done(server=%d): %v", server, err)
	}
}

func assertTrace(t *testing.T, res Result, want []Step) {
	t.Helper()
	if !reflect.DeepEqual(res.Trace, want) {
		t.Fatalf("轨迹 = %v, want %v", res.Trace, want)
	}
}

// stepWant 为单候选欠额序列中一步的期望。
type stepWant struct {
	outcome Outcome
	d       int64
}

// 规格示例：thetaLow=100, thetaNorm=300, dCap=500, p=40, D=0, 单候选, 在途不满。
func TestSpecDeficitSequences(t *testing.T) {
	cases := []struct {
		name string
		msgs []classify.Message
		want []stepWant
	}{
		{"Low连续5条减2条", repeatMsg(lowMsg(), 5), []stepWant{
			{Forwarded, 40}, {Forwarded, 80}, {RejectedOverload, 20},
			{Forwarded, 60}, {RejectedOverload, 0},
		}},
		{"Normal前7条转发第8条减载", repeatMsg(normalMsg(), 10), []stepWant{
			{Forwarded, 40}, {Forwarded, 80}, {Forwarded, 120}, {Forwarded, 160},
			{Forwarded, 200}, {Forwarded, 240}, {Forwarded, 280}, {RejectedOverload, 220},
			{Forwarded, 260}, {RejectedOverload, 200}, // 恰等 θnorm=300 即减载
		}},
		{"Exempt欠额由Low偿还", append(repeatMsg(exemptMsg(), 3), repeatMsg(lowMsg(), 2)...), []stepWant{
			{Forwarded, 40}, {Forwarded, 80}, {Forwarded, 120},
			{RejectedOverload, 60}, {RejectedOverload, 0},
		}},
		{"Normal与Low交替只减Low", alternateMsg(normalMsg(), lowMsg(), 10), []stepWant{
			{Forwarded, 40}, {Forwarded, 80}, {Forwarded, 120}, {RejectedOverload, 60},
			{Forwarded, 100}, {RejectedOverload, 40}, {Forwarded, 80}, {RejectedOverload, 20},
			{Forwarded, 60}, {RejectedOverload, 0},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := mustNew(t, 100, 300, 500, 1000)
			mustAdd(t, c, 1)
			mustReport(t, c, 1, 1, 40, 1_000_000, 0)
			for i, m := range tc.msgs {
				now := int64(i + 1)
				res := mustRoute(t, c, m, []int64{1}, now)
				d := c.servers[1].EffectiveD(now)
				if res.Outcome != tc.want[i].outcome || d != tc.want[i].d {
					t.Fatalf("第%d条: outcome=%s D=%d, want %s D=%d",
						i+1, res.Outcome, d, tc.want[i].outcome, tc.want[i].d)
				}
				cls, _ := classify.Classify(m)
				t.Logf("输入=%s 输出=%s D=%d 判定依据=D=min(D+p,Dcap) 后与门槛比较(恰等即减载)",
					cls, res.Outcome, d)
			}
		})
	}
}

// 连续 20 条 Exempt 后 D 封顶于 Dcap=500 而非 800。
func TestDCapCapsExemptBurst(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 1000)
	mustAdd(t, c, 1)
	mustReport(t, c, 1, 1, 40, 1_000_000, 0)
	for i := 0; i < 20; i++ {
		res := mustRoute(t, c, exemptMsg(), []int64{1}, int64(i+1))
		if res.Outcome != Forwarded {
			t.Fatalf("第%d条 Exempt 应一律转发, got %s", i+1, res.Outcome)
		}
	}
	if d := c.servers[1].EffectiveD(20); d != 500 {
		t.Fatalf("D=%d, want 500 (Dcap 封顶)", d)
	}
	t.Logf("判定依据: D=min(D+p,Dcap), 20*40=800 被封顶为 500")
}

// Low 先于 Normal 被减：同一欠额水平下 Low 减载而 Normal 转发。
func TestLowShedBeforeNormal(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 1000)
	mustAdd(t, c, 1, 2)
	mustReport(t, c, 1, 1, 40, 1_000_000, 0)
	mustReport(t, c, 2, 1, 40, 1_000_000, 0)
	for i := int64(1); i <= 3; i++ {
		mustRoute(t, c, exemptMsg(), []int64{1}, i) // S1 D=120
		mustRoute(t, c, exemptMsg(), []int64{2}, i) // S2 D=120
	}
	resLow := mustRoute(t, c, lowMsg(), []int64{1}, 4)     // D=160 ≥ θlow=100
	resNorm := mustRoute(t, c, normalMsg(), []int64{2}, 4) // D=160 < θnorm=300
	if resLow.Outcome != RejectedOverload {
		t.Fatalf("Low 在 D=160 应被减载, got %s", resLow.Outcome)
	}
	if resNorm.Outcome != Forwarded {
		t.Fatalf("Normal 在 D=160 应转发, got %s", resNorm.Outcome)
	}
	t.Logf("判定依据: θlow=100 ≤ 160 < θnorm=300, Low 减载而 Normal 转发")
}

// 改派示例（W=2）：S1 p=50 D=50，S2 无限制。
func setupReroute(t *testing.T) *Controller {
	t.Helper()
	c := mustNew(t, 100, 300, 500, 2)
	mustAdd(t, c, 1, 2)
	mustReport(t, c, 1, 1, 50, 1_000_000, 0)
	mustRoute(t, c, exemptMsg(), []int64{1}, 1) // S1 D=50
	mustDone(t, c, 1, 2)                        // 释放在途
	return c
}

func TestRerouteAfterDrop(t *testing.T) {
	c := setupReroute(t)
	res := mustRoute(t, c, lowMsg(), []int64{1, 2}, 3)
	assertTrace(t, res, []Step{{Server: 1, Outcome: StepDropped}, {Server: 2, Outcome: StepForwarded}})
	if res.Outcome != Forwarded || res.Target != 2 {
		t.Fatalf("outcome=%s target=%d, want Forwarded 到 2", res.Outcome, res.Target)
	}
	if d := c.servers[1].EffectiveD(3); d != 0 {
		t.Fatalf("改派后 S1 的 D=%d, want 0 (D=100 减载后扣 100)", d)
	}
	t.Logf("判定依据: S1 D=50+50=100 ≥ θlow 减载至 0, 改派 S2 转发")
}

func TestRerouteFailsRejectOverload(t *testing.T) {
	c := setupReroute(t)
	mustRoute(t, c, exemptMsg(), []int64{2}, 3)
	mustRoute(t, c, exemptMsg(), []int64{2}, 4) // S2 在途=2 满
	res := mustRoute(t, c, lowMsg(), []int64{1, 2}, 5)
	assertTrace(t, res, []Step{{Server: 1, Outcome: StepDropped}, {Server: 2, Outcome: StepBusy}})
	if res.Outcome != RejectedOverload {
		t.Fatalf("outcome=%s, want RejectedOverload", res.Outcome)
	}
	if d := c.servers[1].EffectiveD(5); d != 0 {
		t.Fatalf("拒绝后 S1 的 D=%d, want 0 (减载照常生效)", d)
	}
}

func TestAllBusyRejectBusyKeepsD(t *testing.T) {
	c := setupReroute(t)
	mustRoute(t, c, exemptMsg(), []int64{1}, 3)
	mustRoute(t, c, exemptMsg(), []int64{1}, 4) // S1 在途=2 满, D=150
	mustRoute(t, c, exemptMsg(), []int64{2}, 5)
	mustRoute(t, c, exemptMsg(), []int64{2}, 6) // S2 在途=2 满
	res := mustRoute(t, c, lowMsg(), []int64{1, 2}, 7)
	assertTrace(t, res, []Step{{Server: 1, Outcome: StepBusy}, {Server: 2, Outcome: StepBusy}})
	if res.Outcome != RejectedBusy {
		t.Fatalf("outcome=%s, want RejectedBusy", res.Outcome)
	}
	if d := c.servers[1].EffectiveD(7); d != 150 {
		t.Fatalf("全 Busy 时 S1 的 D=%d, want 150 (Busy 不改 D)", d)
	}
	if a := c.servers[1].Arrivals; a != 3 {
		t.Fatalf("S1 到达数=%d, want 3 (Busy 不计到达)", a)
	}
}

// 限制在 e=1000 恰到期：t=999 仍累加，t=1000 起 D 视为 0。
func TestExpiryBoundary(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 1000)
	mustAdd(t, c, 1)
	mustReport(t, c, 1, 1, 40, 1000, 0) // e=1000
	mustRoute(t, c, exemptMsg(), []int64{1}, 999)
	if d := c.servers[1].EffectiveD(999); d != 40 {
		t.Fatalf("t=999 D=%d, want 40 (到期前仍累加)", d)
	}
	mustRoute(t, c, exemptMsg(), []int64{1}, 1000)
	if d := c.servers[1].EffectiveD(1000); d != 0 {
		t.Fatalf("t=1000 D=%d, want 0 (恰等即到期, 不再累加)", d)
	}
	if err := c.Report(1, 1, 10, 1000, 1000); !errors.Is(err, ocstate.ErrStaleReport) {
		t.Fatalf("seq 不更大应报通告过期, got %v", err)
	}
	mustReport(t, c, 1, 2, 40, 1000, 1000) // seq 更大, 从 D=0 起算
	mustRoute(t, c, exemptMsg(), []int64{1}, 1001)
	if d := c.servers[1].EffectiveD(1001); d != 40 {
		t.Fatalf("新通告后 D=%d, want 40 (从 0 起算)", d)
	}
}

// validity=0 撤销清零 与 p=0 通告不清零 的差异。
func TestRevokeVsZeroPercent(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 1000)
	mustAdd(t, c, 1)
	mustReport(t, c, 1, 1, 50, 1_000_000, 0)
	mustRoute(t, c, exemptMsg(), []int64{1}, 1)
	mustRoute(t, c, exemptMsg(), []int64{1}, 2) // D=100
	mustReport(t, c, 1, 2, 50, 0, 3)            // validity=0 撤销
	if d := c.servers[1].EffectiveD(3); d != 0 {
		t.Fatalf("撤销后 D=%d, want 0", d)
	}

	c2 := mustNew(t, 100, 300, 500, 1000)
	mustAdd(t, c2, 1)
	mustReport(t, c2, 1, 1, 50, 1_000_000, 0)
	mustRoute(t, c2, exemptMsg(), []int64{1}, 1)
	mustRoute(t, c2, exemptMsg(), []int64{1}, 2) // D=100
	mustReport(t, c2, 1, 2, 0, 1_000_000, 3)     // p=0 但 validity>0: D 保留
	if d := c2.servers[1].EffectiveD(3); d != 100 {
		t.Fatalf("p=0 通告后 D=%d, want 100 (保留不清)", d)
	}
	res := mustRoute(t, c2, lowMsg(), []int64{1}, 4) // D=100+0=100 ≥ θlow 仍减载
	if res.Outcome != RejectedOverload {
		t.Fatalf("已有欠额仍应触发减载, got %s", res.Outcome)
	}
	if d := c2.servers[1].EffectiveD(4); d != 0 {
		t.Fatalf("减载后 D=%d, want 0 (欠额已还清)", d)
	}
	if res := mustRoute(t, c2, lowMsg(), []int64{1}, 5); res.Outcome != Forwarded {
		t.Fatalf("还清后 p=0 不再累加, 应转发, got %s", res.Outcome)
	}
}

// 拒绝次序：参数非法 > 时钟回退 > 服务器不存在 > 状态类，只报第一个。
func TestRejectionOrdering(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 10)
	mustAdd(t, c, 1)
	mustReport(t, c, 1, 1, 40, 1000, 100) // maxNow=100

	// 参数非法优先于时钟回退（候选重复 + now 回退）。
	if _, err := c.Route(lowMsg(), []int64{1, 1}, 50); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("候选重复应报参数非法, got %v", err)
	}
	// 时钟回退优先于服务器不存在。
	if _, err := c.Route(lowMsg(), []int64{999}, 50); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("now 回退应报时钟回退, got %v", err)
	}
	// 服务器不存在优先于状态类（seq=0 本属通告过期）。
	if err := c.Report(999, 0, 40, 100, 100); !errors.Is(err, ErrServerNotFound) {
		t.Fatalf("未登记服务器应报不存在, got %v", err)
	}
	// 状态类：seq 相等报通告过期。
	if err := c.Report(1, 1, 40, 100, 100); !errors.Is(err, ocstate.ErrStaleReport) {
		t.Fatalf("seq 相等应报通告过期, got %v", err)
	}
	// 状态类：无在途。
	if err := c.Done(1, 100); !errors.Is(err, ocstate.ErrNoInFlight) {
		t.Fatalf("无在途应报 ErrNoInFlight, got %v", err)
	}
	// 参数非法的其他形态。
	if _, err := c.Route(lowMsg(), nil, 100); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("候选为空应报参数非法, got %v", err)
	}
	if _, err := c.Route(classify.Message{Method: "FOO"}, []int64{1}, 100); !errors.Is(err, classify.ErrInvalidParam) {
		t.Fatalf("未知方法应报参数非法, got %v", err)
	}
	if err := c.Report(1, 2, 101, 100, 100); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("percent 越界应报参数非法, got %v", err)
	}
	if err := c.AddServer(1); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("重复登记应报已存在, got %v", err)
	}
	if err := c.AddServer(0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("id 越界应报参数非法, got %v", err)
	}
	if _, err := New(99, 300, 500, 10); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("thetaLow<100 应报参数非法, got %v", err)
	}
	if _, err := New(100, 300, 500, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("W=0 应报参数非法, got %v", err)
	}
	// 被拒绝的操作不推进时钟：now=99 仍报时钟回退，now=100 仍可用。
	if _, err := c.Route(lowMsg(), []int64{1}, 99); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("失败操作不应推进时钟, got %v", err)
	}
	if res := mustRoute(t, c, lowMsg(), []int64{1}, 100); res.Outcome != Forwarded {
		t.Fatalf("now=100 应仍可用, got %s", res.Outcome)
	}
}

// 非导出计数器 touched：一次 Route 触碰的服务器记录数不超过候选数，
// 与已登记服务器总数无关（10 与 10000 台两档对照）。
func TestTouchedBoundedByCandidates(t *testing.T) {
	for _, total := range []int64{10, 10_000} {
		c := mustNew(t, 100, 300, 500, 1)
		for id := int64(1); id <= total; id++ {
			mustAdd(t, c, id)
		}
		res := mustRoute(t, c, lowMsg(), []int64{1, 2, 3}, 1)
		if res.Outcome != Forwarded || c.touched != 1 {
			t.Fatalf("total=%d: 首台转发 touched=%d, want 1", total, c.touched)
		}
		mustRoute(t, c, exemptMsg(), []int64{2}, 2)
		mustRoute(t, c, exemptMsg(), []int64{3}, 3) // 2、3 在途满(W=1)
		mustRoute(t, c, exemptMsg(), []int64{1}, 4) // 1 在途满
		res = mustRoute(t, c, lowMsg(), []int64{1, 2, 3}, 5)
		if res.Outcome != RejectedBusy || c.touched != 3 {
			t.Fatalf("total=%d: 全 Busy touched=%d, want 3 (=候选数)", total, c.touched)
		}
		t.Logf("total=%d touched=%d 判定依据=只触碰候选记录, 与登记总数无关", total, c.touched)
	}
}

// 相同操作序列重放得到相同轨迹。
func TestReplayDeterminism(t *testing.T) {
	run := func() []Result {
		c := mustNew(t, 100, 300, 500, 2)
		mustAdd(t, c, 1, 2)
		mustReport(t, c, 1, 1, 50, 1_000_000, 0)
		mustReport(t, c, 2, 1, 70, 1_000_000, 0)
		msgs := []classify.Message{lowMsg(), normalMsg(), exemptMsg()}
		var out []Result
		for i := 0; i < 30; i++ {
			now := int64(i + 1)
			res := mustRoute(t, c, msgs[i%3], []int64{1, 2}, now)
			out = append(out, res)
			if res.Outcome == Forwarded && i%2 == 0 {
				mustDone(t, c, res.Target, now)
			}
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放轨迹不一致:\n第一次=%v\n第二次=%v", first, second)
	}
}

// 并发调用等价于某个串行顺序：不变量必须始终成立。
func TestConcurrentOpsSerialize(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 4)
	mustAdd(t, c, 1, 2, 3, 4)
	for id := int64(1); id <= 4; id++ {
		mustReport(t, c, id, 1, 30, 1_000_000, 0)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := int64(g%4 + 1)
				res, err := c.Route(normalMsg(), []int64{id}, 10)
				if err != nil {
					t.Errorf("Route: %v", err)
					return
				}
				if res.Outcome == Forwarded {
					_ = c.Done(id, 10)
				}
			}
		}(g)
	}
	wg.Wait()
	for id := int64(1); id <= 4; id++ {
		s := c.servers[id]
		if s.Arrivals != s.Forwarded+s.Dropped {
			t.Errorf("服务器 %d: 到达数 %d != 转发 %d + 减载 %d", id, s.Arrivals, s.Forwarded, s.Dropped)
		}
		if s.InFlight < 0 || s.InFlight > 4 {
			t.Errorf("服务器 %d: 在途数 %d 越界 [0,4]", id, s.InFlight)
		}
		if d := s.EffectiveD(10); d < 0 || d > 500 {
			t.Errorf("服务器 %d: D=%d 越界 [0,500]", id, d)
		}
	}
}
