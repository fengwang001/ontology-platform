package throttle

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/classify"
)

func lowMsg() classify.Message {
	return classify.Message{Method: "OPTIONS", Priority: classify.PriorityNormal}
}

func normalMsg() classify.Message {
	return classify.Message{Method: "INVITE", Priority: classify.PriorityNormal}
}

func exemptMsg() classify.Message {
	return classify.Message{Method: "ACK", Priority: classify.PriorityNormal}
}

func mustNew(t *testing.T, thLow, thNorm, dcap, w int64) *Controller {
	t.Helper()
	c, err := New(thLow, thNorm, dcap, w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// routeSeq 从 startNow 起连续单候选发送 n 条消息，返回每条的 (结果, 发送后欠额)。
func routeSeq(t *testing.T, c *Controller, msg classify.Message, server, n int, startNow int64) ([]Outcome, []int64) {
	t.Helper()
	outs := make([]Outcome, 0, n)
	ds := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		res, err := c.Route(msg, []int{server}, startNow+int64(i))
		must(t, err)
		outs = append(outs, res.Outcome)
		ds = append(ds, c.Inspect(server).D)
	}
	return outs, ds
}

func repeat(o Outcome, n int) []Outcome {
	out := make([]Outcome, n)
	for i := range out {
		out[i] = o
	}
	return out
}

func newExampleController(t *testing.T) *Controller {
	t.Helper()
	c := mustNew(t, 100, 300, 500, 1000)
	must(t, c.AddServer(1))
	must(t, c.Report(1, 1, 40, 1_000_000_000, 0))
	return c
}

// 规格示例：θlow=100, θnorm=300, Dcap=500, p=40。
func TestWorkedExamples(t *testing.T) {
	t.Run("five lows", func(t *testing.T) {
		c := newExampleController(t)
		outs, ds := routeSeq(t, c, lowMsg(), 1, 5, 1)
		wantD := []int64{40, 80, 20, 60, 0}
		wantO := []Outcome{OutcomeForwarded, OutcomeForwarded, OutcomeRejected, OutcomeForwarded, OutcomeRejected}
		if !reflect.DeepEqual(ds, wantD) || !reflect.DeepEqual(outs, wantO) {
			t.Fatalf("D=%v out=%v, want D=%v out=%v", ds, outs, wantD, wantO)
		}
	})

	t.Run("ten normals", func(t *testing.T) {
		c := newExampleController(t)
		outs, ds := routeSeq(t, c, normalMsg(), 1, 10, 1)
		wantD := []int64{40, 80, 120, 160, 200, 240, 280, 220, 260, 200}
		wantO := append(repeat(OutcomeForwarded, 7), OutcomeRejected, OutcomeForwarded, OutcomeRejected)
		if !reflect.DeepEqual(ds, wantD) || !reflect.DeepEqual(outs, wantO) {
			t.Fatalf("D=%v out=%v, want D=%v out=%v", ds, outs, wantD, wantO)
		}
	})

	t.Run("exempt debt repaid by lows", func(t *testing.T) {
		c := newExampleController(t)
		outs, _ := routeSeq(t, c, exemptMsg(), 1, 3, 1)
		if !reflect.DeepEqual(outs, repeat(OutcomeForwarded, 3)) {
			t.Fatalf("exempts: %v", outs)
		}
		if d := c.Inspect(1).D; d != 120 {
			t.Fatalf("D=%d, want 120", d)
		}
		outs, ds := routeSeq(t, c, lowMsg(), 1, 2, 4)
		if !reflect.DeepEqual(outs, repeat(OutcomeRejected, 2)) || !reflect.DeepEqual(ds, []int64{60, 0}) {
			t.Fatalf("lows out=%v D=%v, want both rejected, D=[60 0]", outs, ds)
		}
	})

	t.Run("alternating normal and low", func(t *testing.T) {
		c := newExampleController(t)
		var outs []Outcome
		var ds []int64
		for i := 0; i < 10; i++ {
			msg := normalMsg()
			if i%2 == 1 {
				msg = lowMsg()
			}
			res, err := c.Route(msg, []int{1}, int64(i+1))
			must(t, err)
			outs = append(outs, res.Outcome)
			ds = append(ds, c.Inspect(1).D)
		}
		wantD := []int64{40, 80, 120, 60, 100, 40, 80, 20, 60, 0}
		wantO := []Outcome{
			OutcomeForwarded, OutcomeForwarded, OutcomeForwarded, OutcomeRejected, OutcomeForwarded,
			OutcomeRejected, OutcomeForwarded, OutcomeRejected, OutcomeForwarded, OutcomeRejected,
		}
		if !reflect.DeepEqual(ds, wantD) || !reflect.DeepEqual(outs, wantO) {
			t.Fatalf("D=%v out=%v, want D=%v out=%v", ds, outs, wantD, wantO)
		}
	})

	t.Run("dcap caps exempt burst", func(t *testing.T) {
		c := newExampleController(t)
		outs, _ := routeSeq(t, c, exemptMsg(), 1, 20, 1)
		if !reflect.DeepEqual(outs, repeat(OutcomeForwarded, 20)) {
			t.Fatalf("exempts: %v", outs)
		}
		if d := c.Inspect(1).D; d != 500 {
			t.Fatalf("D=%d, want 500 (capped, not 800)", d)
		}
	})
}

// 规格示例：改派，W=2，S1 的 p=50、D=50，S2 无限制。
func setupReroute(t *testing.T) *Controller {
	t.Helper()
	c := mustNew(t, 100, 300, 500, 2)
	must(t, c.AddServer(1))
	must(t, c.AddServer(2))
	must(t, c.Report(1, 1, 50, 1_000_000_000, 0))
	res, err := c.Route(exemptMsg(), []int{1}, 1) // S1: D=50, inflight=1
	must(t, err)
	if res.Outcome != OutcomeForwarded {
		t.Fatalf("setup forward: %+v", res)
	}
	must(t, c.Done(1, 2)) // inflight 回到 0
	return c
}

func TestReroute(t *testing.T) {
	t.Run("dropped then forwarded by next candidate", func(t *testing.T) {
		c := setupReroute(t)
		res, err := c.Route(lowMsg(), []int{1, 2}, 3)
		must(t, err)
		want := []Step{{Server: 1, Action: ActionDropped}, {Server: 2, Action: ActionForwarded}}
		if res.Outcome != OutcomeForwarded || res.Server != 2 || !reflect.DeepEqual(res.Trail, want) {
			t.Fatalf("got %+v, want forwarded to 2 with trail %v", res, want)
		}
		// 改派后前一台的 D 已变。
		if d := c.Inspect(1).D; d != 0 {
			t.Fatalf("S1 D=%d, want 0", d)
		}
	})

	t.Run("dropped then busy rejects with overload", func(t *testing.T) {
		c := setupReroute(t)
		mustRoute := func(now int64) {
			res, err := c.Route(exemptMsg(), []int{2}, now)
			must(t, err)
			if res.Outcome != OutcomeForwarded {
				t.Fatalf("fill S2: %+v", res)
			}
		}
		mustRoute(3)
		mustRoute(4) // S2 inflight=2=W
		res, err := c.Route(lowMsg(), []int{1, 2}, 5)
		must(t, err)
		want := []Step{{Server: 1, Action: ActionDropped}, {Server: 2, Action: ActionBusy}}
		if res.Outcome != OutcomeRejected || res.Reason != ReasonOverload || !reflect.DeepEqual(res.Trail, want) {
			t.Fatalf("got %+v, want rejected overload with trail %v", res, want)
		}
		if d := c.Inspect(1).D; d != 0 {
			t.Fatalf("S1 D=%d, want 0", d)
		}
	})

	t.Run("all busy rejects with busy and keeps D", func(t *testing.T) {
		c := setupReroute(t)
		// p 改为 0：D 保留 50，继续到达不再累加。
		must(t, c.Report(1, 2, 0, 1_000_000_000, 3))
		for now := int64(4); now <= 5; now++ { // S1 inflight=2=W，D 仍 50
			res, err := c.Route(exemptMsg(), []int{1}, now)
			must(t, err)
			if res.Outcome != OutcomeForwarded {
				t.Fatalf("fill S1: %+v", res)
			}
		}
		for now := int64(6); now <= 7; now++ {
			res, err := c.Route(exemptMsg(), []int{2}, now)
			must(t, err)
			if res.Outcome != OutcomeForwarded {
				t.Fatalf("fill S2: %+v", res)
			}
		}
		before := c.Inspect(1)
		res, err := c.Route(lowMsg(), []int{1, 2}, 8)
		must(t, err)
		want := []Step{{Server: 1, Action: ActionBusy}, {Server: 2, Action: ActionBusy}}
		if res.Outcome != OutcomeRejected || res.Reason != ReasonBusy || !reflect.DeepEqual(res.Trail, want) {
			t.Fatalf("got %+v, want rejected busy with trail %v", res, want)
		}
		after := c.Inspect(1)
		// Busy 不改 D 且先于到达计数。
		if after.D != 50 || after.Arrivals != before.Arrivals {
			t.Fatalf("busy must not change D/arrivals: before=%+v after=%+v", before, after)
		}
	})
}

// 规格示例：限制在 e=1000 到期。
func TestExpiryBoundary(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 10)
	must(t, c.AddServer(1))
	must(t, c.Report(1, 1, 40, 1000, 0)) // e=1000

	res, err := c.Route(lowMsg(), []int{1}, 999) // t=999 仍按 p 累加
	must(t, err)
	if res.Outcome != OutcomeForwarded || c.Inspect(1).D != 40 {
		t.Fatalf("t=999: %+v D=%d, want forwarded D=40", res, c.Inspect(1).D)
	}
	res, err = c.Route(lowMsg(), []int{1}, 1000) // t=1000 恰到期：D 视为 0 且不再累加
	must(t, err)
	if res.Outcome != OutcomeForwarded || c.Inspect(1).D != 0 {
		t.Fatalf("t=1000: %+v D=%d, want forwarded D=0", res, c.Inspect(1).D)
	}
	// seq 更大的新通告从 D=0 起算。
	must(t, c.Report(1, 2, 100, 1000, 1000))
	res, err = c.Route(lowMsg(), []int{1}, 1001)
	must(t, err)
	if res.Outcome != OutcomeRejected || res.Reason != ReasonOverload || c.Inspect(1).D != 0 {
		t.Fatalf("t=1001: %+v D=%d, want rejected overload D=0", res, c.Inspect(1).D)
	}
	// seq 不更大（相等）报通告过期。
	if err := c.Report(1, 2, 50, 100, 1002); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("equal seq err=%v, want ErrStaleReport", err)
	}
}

// validity=0 撤销清零 vs p=0 保留欠额。
func TestRevokeVsZeroPercent(t *testing.T) {
	setup := func(t *testing.T) *Controller {
		c := mustNew(t, 100, 300, 500, 10)
		must(t, c.AddServer(1))
		must(t, c.Report(1, 1, 50, 1_000_000_000, 0))
		routeSeq(t, c, exemptMsg(), 1, 2, 1) // D=100
		return c
	}

	t.Run("revoke clears deficit", func(t *testing.T) {
		c := setup(t)
		must(t, c.Report(1, 2, 0, 0, 3)) // validity=0 撤销
		if snap := c.Inspect(1); snap.P != 0 || snap.D != 0 {
			t.Fatalf("after revoke: p=%d d=%d, want 0/0", snap.P, snap.D)
		}
		res, err := c.Route(lowMsg(), []int{1}, 4)
		must(t, err)
		if res.Outcome != OutcomeForwarded {
			t.Fatalf("low after revoke: %+v, want forwarded", res)
		}
	})

	t.Run("zero percent keeps deficit", func(t *testing.T) {
		c := setup(t)
		must(t, c.Report(1, 2, 0, 1_000_000_000, 3)) // p=0 但 D 保留
		if snap := c.Inspect(1); snap.P != 0 || snap.D != 100 {
			t.Fatalf("after p=0 report: p=%d d=%d, want 0/100", snap.P, snap.D)
		}
		// D 不再增加，已有欠额仍触发减载直到还清。
		res, err := c.Route(lowMsg(), []int{1}, 4)
		must(t, err)
		if res.Outcome != OutcomeRejected || res.Reason != ReasonOverload || c.Inspect(1).D != 0 {
			t.Fatalf("low with leftover debt: %+v D=%d, want rejected overload D=0", res, c.Inspect(1).D)
		}
		res, err = c.Route(lowMsg(), []int{1}, 5)
		must(t, err)
		if res.Outcome != OutcomeForwarded {
			t.Fatalf("debt repaid: %+v, want forwarded", res)
		}
	})
}

func TestRouteErrors(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 2)
	must(t, c.AddServer(1))
	must(t, c.AddServer(2))
	must(t, c.Report(1, 1, 100, 1_000_000_000, 0))

	nine := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	paramCases := []struct {
		name  string
		msg   classify.Message
		cands []int
	}{
		{"no candidates", normalMsg(), nil},
		{"nine candidates", normalMsg(), nine},
		{"duplicate candidates", normalMsg(), []int{1, 1}},
		{"candidate id zero", normalMsg(), []int{0}},
		{"candidate id too large", normalMsg(), []int{1_000_001}},
		{"unknown method", classify.Message{Method: "FOO", Priority: classify.PriorityNormal}, []int{1}},
	}
	for _, tc := range paramCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Route(tc.msg, tc.cands, 1); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("err=%v, want ErrInvalidParam", err)
			}
		})
	}

	if _, err := c.Route(normalMsg(), []int{3}, 1); !errors.Is(err, ErrServerNotExist) {
		t.Fatalf("unknown candidate err=%v, want ErrServerNotExist", err)
	}
	// 出错的 Route 不推进时钟：now=1 仍被接受。
	if _, err := c.Route(exemptMsg(), []int{1}, 1); err != nil {
		t.Fatalf("clock must not advance on error: %v", err)
	}

	// 推进时钟到 10。
	mustRouteAt(t, c, 10)
	// 参数非法 > 时钟回退：候选重复且 now 回退。
	if _, err := c.Route(normalMsg(), []int{1, 1}, 5); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("param>clock err=%v", err)
	}
	// 时钟回退 > 服务器不存在。
	if _, err := c.Route(normalMsg(), []int{3}, 5); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock>notexist err=%v", err)
	}
	if _, err := c.Route(normalMsg(), []int{1}, 5); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock back err=%v", err)
	}
}

func mustRouteAt(t *testing.T, c *Controller, now int64) {
	t.Helper()
	if _, err := c.Route(exemptMsg(), []int{1}, now); err != nil {
		t.Fatalf("route at %d: %v", now, err)
	}
}

// 拒绝是结果而非错误：D 改动生效、时钟照常推进。
func TestRejectedRouteAdvancesClock(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 10)
	must(t, c.AddServer(1))
	must(t, c.Report(1, 1, 100, 1_000_000_000, 0))
	res, err := c.Route(lowMsg(), []int{1}, 50) // D=100 减载，单候选拒绝
	must(t, err)
	if res.Outcome != OutcomeRejected || res.Reason != ReasonOverload {
		t.Fatalf("got %+v, want rejected overload", res)
	}
	if d := c.Inspect(1).D; d != 0 {
		t.Fatalf("D=%d, want 0 after drop", d)
	}
	if err := c.Report(1, 2, 50, 100, 49); !errors.Is(err, ErrClockBack) {
		t.Fatalf("rejected route must advance clock: err=%v", err)
	}
}

// touched：一次 Route 触碰的记录数不超过候选数，与登记总数无关。
func TestTouchedIndependentOfRegistry(t *testing.T) {
	for _, total := range []int{10, 10_000} {
		t.Run(fmt.Sprintf("servers=%d", total), func(t *testing.T) {
			c := mustNew(t, 100, 300, 500, 10)
			for i := 1; i <= total; i++ {
				must(t, c.AddServer(i))
			}
			must(t, c.Report(1, 1, 100, 1_000_000_000, 0))
			must(t, c.Report(2, 1, 100, 1_000_000_000, 0))
			res, err := c.Route(lowMsg(), []int{1, 2, 3}, 1)
			must(t, err)
			if res.Outcome != OutcomeForwarded || res.Server != 3 {
				t.Fatalf("got %+v, want forwarded to 3", res)
			}
			if c.touched > 3 {
				t.Fatalf("touched=%d with %d servers, want <= 3", c.touched, total)
			}
		})
	}
}

// 并发调用等价于某个串行顺序（配合 -race 验证）。
func TestConcurrent(t *testing.T) {
	c := mustNew(t, 100, 300, 500, 4)
	for i := 1; i <= 8; i++ {
		must(t, c.AddServer(i))
	}
	var wg sync.WaitGroup
	for g := 1; g <= 8; g++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				res, err := c.Route(normalMsg(), []int{id}, 0)
				if err != nil {
					t.Errorf("route: %v", err)
					return
				}
				if res.Outcome == OutcomeForwarded {
					if err := c.Done(id, 0); err != nil {
						t.Errorf("done: %v", err)
						return
					}
				}
			}
		}(g)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if err := c.Report(id, int64(i+1), 30, 1_000_000, 0); err != nil {
					t.Errorf("report: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	for id := 1; id <= 8; id++ {
		snap := c.Inspect(id)
		if snap.Inflight != 0 {
			t.Errorf("server %d inflight=%d, want 0", id, snap.Inflight)
		}
		if snap.Arrivals != snap.Forwarded+snap.Dropped {
			t.Errorf("server %d arrivals=%d != forwarded=%d+dropped=%d",
				id, snap.Arrivals, snap.Forwarded, snap.Dropped)
		}
		if snap.D < 0 || snap.D > 500 {
			t.Errorf("server %d D=%d out of [0,500]", id, snap.D)
		}
	}
}

// ---------- 朴素模拟：逐条按规则独立实现，用于随机对照 ----------

type mServer struct {
	maxSeq, p, e, d, inflight int64
}

type model struct {
	thLow, thNorm, dcap, w int64
	servers                map[int]*mServer
	maxNow                 int64
	hasNow                 bool
}

func newModel(thLow, thNorm, dcap, w int64) *model {
	return &model{thLow: thLow, thNorm: thNorm, dcap: dcap, w: w, servers: map[int]*mServer{}}
}

func (m *model) checkNow(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if m.hasNow && now < m.maxNow {
		return ErrClockBack
	}
	return nil
}

func (m *model) advance(now int64) {
	if !m.hasNow || now > m.maxNow {
		m.maxNow, m.hasNow = now, true
	}
}

func (m *model) addServer(id int) error {
	if id < 1 || id > 1_000_000 {
		return ErrInvalidParam
	}
	if _, ok := m.servers[id]; ok {
		return ErrServerExists
	}
	m.servers[id] = &mServer{maxSeq: -1}
	return nil
}

func (m *model) report(id int, seq, pct, val, now int64) error {
	if id < 1 || id > 1_000_000 || seq < 0 || pct < 0 || pct > 100 || val < 0 || val > 1_000_000_000 {
		return ErrInvalidParam
	}
	if err := m.checkNow(now); err != nil {
		return err
	}
	s, ok := m.servers[id]
	if !ok {
		return ErrServerNotExist
	}
	if seq <= s.maxSeq {
		return ErrStaleReport
	}
	if now >= s.e {
		s.p, s.d = 0, 0
	}
	s.maxSeq = seq
	if val == 0 {
		s.p, s.d, s.e = 0, 0, now
	} else {
		s.p, s.e = pct, now+val
	}
	m.advance(now)
	return nil
}

func (m *model) done(id int, now int64) error {
	if id < 1 || id > 1_000_000 {
		return ErrInvalidParam
	}
	if err := m.checkNow(now); err != nil {
		return err
	}
	s, ok := m.servers[id]
	if !ok {
		return ErrServerNotExist
	}
	if s.inflight == 0 {
		return ErrNoInflight
	}
	s.inflight--
	m.advance(now)
	return nil
}

func naiveClassify(msg classify.Message) (classify.Class, error) {
	switch msg.Method {
	case "ACK", "BYE", "CANCEL", "PRACK", "OPTIONS", "SUBSCRIBE", "MESSAGE",
		"INVITE", "REGISTER", "UPDATE", "REFER", "NOTIFY", "PUBLISH", "INFO":
	default:
		return 0, ErrInvalidParam
	}
	switch msg.Priority {
	case classify.PriorityEmergency, classify.PriorityNormal, classify.PriorityNonUrgent:
	default:
		return 0, ErrInvalidParam
	}
	if msg.InDialog || msg.Priority == classify.PriorityEmergency {
		return classify.Exempt, nil
	}
	switch msg.Method {
	case "ACK", "BYE", "CANCEL", "PRACK":
		return classify.Exempt, nil
	}
	if msg.Priority == classify.PriorityNonUrgent {
		return classify.Low, nil
	}
	switch msg.Method {
	case "OPTIONS", "SUBSCRIBE", "MESSAGE":
		return classify.Low, nil
	}
	return classify.Normal, nil
}

func (m *model) route(msg classify.Message, cands []int, now int64) (Result, error) {
	class, err := naiveClassify(msg)
	if err != nil {
		return Result{}, err
	}
	if len(cands) < 1 || len(cands) > 8 {
		return Result{}, ErrInvalidParam
	}
	seen := map[int]bool{}
	for _, id := range cands {
		if id < 1 || id > 1_000_000 || seen[id] {
			return Result{}, ErrInvalidParam
		}
		seen[id] = true
	}
	if err := m.checkNow(now); err != nil {
		return Result{}, err
	}
	for _, id := range cands {
		if _, ok := m.servers[id]; !ok {
			return Result{}, ErrServerNotExist
		}
	}
	var trail []Step
	dropped := false
	for _, id := range cands {
		s := m.servers[id]
		if s.inflight >= m.w {
			trail = append(trail, Step{Server: id, Action: ActionBusy})
			continue
		}
		if now >= s.e {
			s.p, s.d = 0, 0
		}
		s.d += s.p
		if s.d > m.dcap {
			s.d = m.dcap
		}
		fwd := false
		switch class {
		case classify.Exempt:
			fwd = true
		case classify.Low:
			if s.d >= m.thLow {
				s.d -= 100
				trail = append(trail, Step{Server: id, Action: ActionDropped})
				dropped = true
			} else {
				fwd = true
			}
		case classify.Normal:
			if s.d >= m.thNorm {
				s.d -= 100
				trail = append(trail, Step{Server: id, Action: ActionDropped})
				dropped = true
			} else {
				fwd = true
			}
		}
		if fwd {
			s.inflight++
			trail = append(trail, Step{Server: id, Action: ActionForwarded})
			m.advance(now)
			return Result{Outcome: OutcomeForwarded, Server: id, Trail: trail}, nil
		}
	}
	m.advance(now)
	if dropped {
		return Result{Outcome: OutcomeRejected, Reason: ReasonOverload, Trail: trail}, nil
	}
	return Result{Outcome: OutcomeRejected, Reason: ReasonBusy, Trail: trail}, nil
}

func sameResult(a, b Result) bool {
	if a.Outcome != b.Outcome || a.Server != b.Server || a.Reason != b.Reason || len(a.Trail) != len(b.Trail) {
		return false
	}
	for i := range a.Trail {
		if a.Trail[i] != b.Trail[i] {
			return false
		}
	}
	return true
}

// 1500 组随机操作序列与朴素模拟逐步对照。
func TestRandomSimulation(t *testing.T) {
	methods := []string{"ACK", "BYE", "CANCEL", "PRACK", "OPTIONS", "SUBSCRIBE", "MESSAGE",
		"INVITE", "REGISTER", "UPDATE", "REFER", "NOTIFY", "PUBLISH", "INFO"}
	prios := []classify.Priority{classify.PriorityEmergency, classify.PriorityNormal, classify.PriorityNonUrgent}
	const pool = 12

	for seq := 0; seq < 1500; seq++ {
		t.Run(fmt.Sprintf("seq=%d", seq), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
			thLow := 100 + rng.Int63n(200)
			thNorm := thLow + rng.Int63n(300)
			dcap := thNorm + rng.Int63n(700)
			w := 1 + rng.Int63n(4)
			c, err := New(thLow, thNorm, dcap, w)
			must(t, err)
			m := newModel(thLow, thNorm, dcap, w)
			t.Logf("params thLow=%d thNorm=%d dcap=%d w=%d", thLow, thNorm, dcap, w)

			genNow := int64(0)
			ops := 25 + rng.Intn(20)
			for op := 0; op < ops; op++ {
				now := genNow
				clockBack := false
				switch r := rng.Intn(10); {
				case r < 7:
					now = genNow + rng.Int63n(80)
				case r < 9:
					now = genNow
				default:
					if genNow > 0 {
						now = genNow - 1 - rng.Int63n(5)
						clockBack = true
					}
				}

				var errC, errM error
				var desc, out string
				hasClock := true
				kind := rng.Intn(100)
				switch {
				case kind < 10:
					hasClock = false
					id := 1 + rng.Intn(pool)
					errC = c.AddServer(id)
					errM = m.addServer(id)
					desc = fmt.Sprintf("AddServer(%d)", id)
				case kind < 40:
					id := 1 + rng.Intn(pool+1)
					rseq := int64(rng.Intn(8))
					pct := int64(rng.Intn(105))
					var val int64
					if rng.Intn(7) == 0 {
						val = 0
					} else {
						val = 1 + rng.Int63n(3000)
					}
					errC = c.Report(id, rseq, pct, val, now)
					errM = m.report(id, rseq, pct, val, now)
					desc = fmt.Sprintf("Report(id=%d seq=%d p=%d v=%d now=%d)", id, rseq, pct, val, now)
				case kind < 55:
					id := 1 + rng.Intn(pool+1)
					errC = c.Done(id, now)
					errM = m.done(id, now)
					desc = fmt.Sprintf("Done(id=%d now=%d)", id, now)
				default:
					msg := classify.Message{
						Method:   methods[rng.Intn(len(methods))],
						InDialog: rng.Intn(2) == 0,
						Priority: prios[rng.Intn(len(prios))],
					}
					if rng.Intn(12) == 0 {
						msg.Method = "FOO"
					}
					if rng.Intn(15) == 0 {
						msg.Priority = "bogus"
					}
					var cands []int
					switch r := rng.Intn(20); {
					case r == 0:
						cands = nil
					case r == 1:
						cands = []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
					default:
						k := 1 + rng.Intn(8)
						perm := rng.Perm(pool)
						for i := 0; i < k; i++ {
							cands = append(cands, perm[i]+1)
						}
						if rng.Intn(10) == 0 && len(cands) > 0 {
							cands[len(cands)-1] = 500 // 未登记候选
						}
						if rng.Intn(12) == 0 && len(cands) > 1 {
							cands[len(cands)-1] = cands[0] // 重复候选
						}
					}
					resC, e1 := c.Route(msg, cands, now)
					resM, e2 := m.route(msg, cands, now)
					errC, errM = e1, e2
					desc = fmt.Sprintf("Route(msg=%+v cands=%v now=%d)", msg, cands, now)
					if e1 == nil && e2 == nil && !sameResult(resC, resM) {
						t.Fatalf("op=%d %s\nctrl=%+v\nmodel=%+v", op, desc, resC, resM)
					}
					out = fmt.Sprintf(" -> %+v", resC)
				}

				if (errC == nil) != (errM == nil) {
					t.Fatalf("op=%d %s: ctrl err=%v, model err=%v", op, desc, errC, errM)
				}
				if errM != nil && !errors.Is(errC, errM) {
					t.Fatalf("op=%d %s: ctrl err=%v, model err=%v", op, desc, errC, errM)
				}
				if errC == nil && hasClock && !clockBack {
					genNow = now
				}
				t.Logf("op=%d in=%s out=%s err=%v", op, desc, out, errC)

				for id := 1; id <= pool; id++ {
					snap := c.Inspect(id)
					ms, ok := m.servers[id]
					if ok != snap.Exists {
						t.Fatalf("op=%d %s: server %d exists ctrl=%v model=%v", op, desc, id, snap.Exists, ok)
					}
					if !ok {
						continue
					}
					if snap.MaxSeq != ms.maxSeq || snap.P != ms.p || snap.ExpiresAt != ms.e ||
						snap.D != ms.d || snap.Inflight != ms.inflight {
						t.Fatalf("op=%d %s: server %d ctrl=%+v model=%+v", op, desc, id, snap, ms)
					}
					if snap.D < 0 || snap.D > dcap {
						t.Fatalf("op=%d %s: server %d D=%d out of [0,%d]", op, desc, id, snap.D, dcap)
					}
					if snap.Inflight < 0 || snap.Inflight > w {
						t.Fatalf("op=%d %s: server %d inflight=%d out of [0,%d]", op, desc, id, snap.Inflight, w)
					}
					if snap.Arrivals != snap.Forwarded+snap.Dropped {
						t.Fatalf("op=%d %s: server %d arrivals=%d != forwarded=%d+dropped=%d",
							op, desc, id, snap.Arrivals, snap.Forwarded, snap.Dropped)
					}
				}
			}
		})
	}
}
