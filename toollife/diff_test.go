package toollife_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/toollife"
	"ontology/toollife/naive"
)

// diffOp 是一条随机生成的操作，带调用通道标签。
type diffOp struct {
	ch      int
	kind    naive.ReqKind
	group   string
	tool    string
	newTool string
	req     string
	amount  uint64
}

func prodCode(err error) string {
	if err == nil {
		return ""
	}
	e := asErr(err)
	switch e.Code {
	case toollife.ErrInvalid:
		return "invalid"
	case toollife.ErrGroupNotFound:
		return "group"
	case toollife.ErrToolNotFound:
		return "tool"
	case toollife.ErrConflict:
		return "conflict"
	case toollife.ErrState:
		return "state"
	case toollife.ErrRequestNotFound:
		return "reqnotfound"
	case toollife.ErrNoTool:
		return "notool"
	case toollife.ErrNoCapacity:
		return "nocapacity"
	}
	return "unknown"
}

// runProd 对生产服务执行一条操作，返回 (是否成功, 错误码, 选中刀)。
func runProd(s *toollife.Service, op diffOp) (bool, string, string) {
	switch op.kind {
	case naive.KApply:
		r, err := s.Apply(op.req, op.group, op.amount)
		return err == nil, prodCode(err), r.ToolID
	case naive.KSettle:
		err := s.Settle(op.req, op.amount)
		return err == nil, prodCode(err), ""
	case naive.KAbort:
		err := s.Abort(op.req)
		return err == nil, prodCode(err), ""
	case naive.KBroken:
		err := s.ReportBroken(op.group, op.tool)
		return err == nil, prodCode(err), ""
	case naive.KReplace:
		err := s.Replace(op.group, op.tool, op.newTool)
		return err == nil, prodCode(err), ""
	case naive.KLock:
		err := s.Lock(op.group, op.tool)
		return err == nil, prodCode(err), ""
	case naive.KUnlock:
		err := s.Unlock(op.group, op.tool)
		return err == nil, prodCode(err), ""
	}
	return false, "unknown", ""
}

func toNaive(op diffOp) naive.Op {
	return naive.Op{Kind: op.kind, Group: op.group, Tool: op.tool, NewTool: op.newTool, Req: op.req, Amount: op.amount}
}

// prodOutcome 是生产服务对一条操作的可观察结果。
type prodOutcome struct {
	ok     bool
	code   string
	toolID string
}

func runNaive(m *naive.Model, op diffOp) prodOutcome {
	r := m.Exec(toNaive(op))
	return prodOutcome{ok: r.OK, code: r.Code, toolID: r.ToolID}
}

// genPlan 生成随机多通道操作计划。
// 工具数 ntools；opCount 条操作；channels 个通道共享同一申请号空间。
func genPlan(rng *rand.Rand, opCount, ntools int) []diffOp {
	const gid = "g"
	ops := make([]diffOp, opCount)
	nextTool := ntools
	for i := range ops {
		ch := rng.Intn(4)
		req := fmt.Sprintf("ch%d-r%d", ch, rng.Intn(40)) // 故意制造重复申请号 -> 幂等/冲突
		toolIdx := rng.Intn(ntools + nextTool)
		tool := toolID(toolIdx)
		switch rng.Intn(100) {
		case 0, 1, 2: // 3% 破损
			ops[i] = diffOp{ch: ch, kind: naive.KBroken, group: gid, tool: tool}
		case 3, 4: // 2% 换新
			ops[i] = diffOp{ch: ch, kind: naive.KReplace, group: gid, tool: tool, newTool: toolID(nextTool)}
			nextTool++
		case 5, 6: // 2% 锁定/解锁
			k := naive.KLock
			if rng.Intn(2) == 1 {
				k = naive.KUnlock
			}
			ops[i] = diffOp{ch: ch, kind: k, group: gid, tool: tool}
		case 7, 8, 9, 10, 11: // 5% 中止
			ops[i] = diffOp{ch: ch, kind: naive.KAbort, req: req}
		case 12, 13, 14, 15, 16, 17, 18, 19, 20, 21: // 10% 记账
			ops[i] = diffOp{ch: ch, kind: naive.KSettle, req: req, amount: uint64(1 + rng.Intn(120))}
		default: // 78% 申请
			ops[i] = diffOp{ch: ch, kind: naive.KApply, group: gid, req: req, amount: uint64(1 + rng.Intn(60))}
		}
	}
	return ops
}

func toolID(i int) string { return fmt.Sprintf("t%02d", i) }

// stateMismatch 比较生产服务最终状态与朴素模型最终状态。
func stateMismatch(t *testing.T, s *toollife.Service, m *naive.Model) string {
	t.Helper()
	snap, err := s.Query("g")
	if err != nil {
		return fmt.Sprintf("prod query err: %v", err)
	}
	want := m.State("g")
	if len(snap.Order) != len(want) {
		return fmt.Sprintf("tool count: prod=%d naive=%d", len(snap.Order), len(want))
	}
	statusMap := map[naive.Status]toollife.ToolStatus{
		naive.Avail: toollife.StatusAvailable, naive.Broken: toollife.StatusBroken,
		naive.Exhausted: toollife.StatusExhausted, naive.Locked: toollife.StatusLocked,
	}
	for i, ts := range want {
		ps := snap.Order[i]
		if ps.ID != ts.ID || ps.Status != statusMap[ts.Status] || ps.Used != ts.Used || ps.Reserved != ts.Reserved {
			return fmt.Sprintf("tool[%d]: prod={%s %d used=%d res=%d} naive={%s %d used=%d res=%d}",
				i, ps.ID, ps.Status, ps.Used, ps.Reserved, ts.ID, ts.Status, ts.Used, ts.Reserved)
		}
	}
	if len(s.Warnings()) != len(m.Warnings()) {
		return fmt.Sprintf("warning count: prod=%d naive=%d", len(s.Warnings()), len(m.Warnings()))
	}
	return ""
}

// TestDifferentialSerialReplay 对每条随机计划：
//  1. 朴素模型按计划原始顺序串行执行，记录每步输出；
//  2. 生产服务按完全相同的顺序串行执行（无并发干扰），逐码逐刀对照；
//  3. 比较最终刀具状态与预警序列。
func TestDifferentialSerialReplay(t *testing.T) {
	const (
		iterations = 300
		opCount    = 120
		ntools     = 4
		limit      = 100
	)
	rng := rand.New(rand.NewSource(20261006))
	for it := 0; it < iterations; it++ {
		seed := rng.Int63()
		plan := genPlan(rand.New(rand.NewSource(seed)), opCount, ntools)

		m := naive.New("g", naive.Cfg{Limit: limit, Warn: 800, Mode: naive.Strict}, toolIDs(ntools))
		svc := newSvc(t, "g", strictCfg(limit), toolIDs(ntools)...)

		for i, op := range plan {
			n := runNaive(m, op)
			ok, code, tid := runProd(svc, op)
			p := prodOutcome{ok: ok, code: code, toolID: tid}
			if p != n {
				t.Fatalf("iter=%d seed=%d step=%d op=%+v\n  naive=%+v\n  prod =%+v",
					it, seed, i, op, n, p)
			}
		}
		if diff := stateMismatch(t, svc, m); diff != "" {
			t.Fatalf("iter=%d seed=%d final state mismatch: %s", it, seed, diff)
		}
	}
}

// genApplyOnlyPlan 生成仅含申请、申请号全局唯一的计划：
// 全部申请被处理后，成功集合的预占总量与选刀结果与提交顺序无关，
// 因而可直接与朴素串行重放对照最终状态。
func genApplyOnlyPlan(rng *rand.Rand, opCount, channels int, gid string) []diffOp {
	ops := make([]diffOp, opCount)
	for i := range ops {
		ops[i] = diffOp{
			ch:     rng.Intn(channels),
			kind:   naive.KApply,
			group:  gid,
			req:    fmt.Sprintf("uniq-%d", i),
			amount: uint64(1 + rng.Intn(30)),
		}
	}
	return ops
}

// TestDifferentialConcurrentEquivalence 多通道并发执行同一计划。
// 全部为唯一申请号的纯申请，最终被接受的申请集合与提交顺序无关，
// 因而最终状态必须等于朴素模型的任意串行重放；用 -race 验证并发安全。
func TestDifferentialConcurrentEquivalence(t *testing.T) {
	const (
		iterations = 60
		opCount    = 200
		ntools     = 4
		limit      = 100
		channels   = 4
	)
	rng := rand.New(rand.NewSource(424242))
	for it := 0; it < iterations; it++ {
		seed := rng.Int63()
		plan := genApplyOnlyPlan(rand.New(rand.NewSource(seed)), opCount, channels, "g")

		svc := newSvc(t, "g", strictCfg(limit), toolIDs(ntools)...)
		m := naive.New("g", naive.Cfg{Limit: limit, Warn: 800, Mode: naive.Strict}, toolIDs(ntools))

		// 按通道分流，通道内严格保序。
		chOps := make([][]diffOp, channels)
		for _, op := range plan {
			chOps[op.ch] = append(chOps[op.ch], op)
		}

		maxLen := 0
		for _, ops := range chOps {
			if len(ops) > maxLen {
				maxLen = len(ops)
			}
		}

		// 令牌环：持有令牌的通道提交其下一条申请，再把令牌传给下一通道；
		// 已耗尽操作的通道空转传递。由此交织顺序确定为 0,1,2,3,0,1,...，
		// 朴素模型在临界区内按同一顺序重放，逐步对照输出、最终对照状态。
		token := make(chan int)
		ack := make(chan struct{}, 1)
		quit := make(chan struct{})
		finished := make(chan struct{}, channels)
		for ch := 0; ch < channels; ch++ {
			go func(ch int) {
				ops := chOps[ch]
				done := false
				for step := range token {
					if step%channels == ch && len(ops) > 0 {
						op := ops[0]
						ops = ops[1:]
						ok, code, tid := runProd(svc, op)
						n := runNaive(m, op)
						if (prodOutcome{ok: ok, code: code, toolID: tid}) != n {
							t.Errorf("iter=%d seed=%d ch=%d op=%+v naive=%+v prod={%v %s %s}",
								it, seed, ch, op, n, ok, code, tid)
						}
					}
					if step+1 >= maxLen*channels {
						done = true
					}
					select {
					case ack <- struct{}{}:
					case <-quit:
						finished <- struct{}{}
						return
					}
					if done {
						finished <- struct{}{}
						return
					}
				}
				finished <- struct{}{}
			}(ch)
		}
		go func() {
			for step := 0; step < maxLen*channels; step++ {
				token <- step // 4 个 goroutine 竞争接收，恰有一个处理本步
				<-ack
			}
			close(token)
		}()
		<-finished // 处理最后一步的 goroutine 退出
		close(quit)
		for ch := 1; ch < channels; ch++ {
			<-finished
		}

		if diff := stateMismatch(t, svc, m); diff != "" {
			t.Fatalf("iter=%d seed=%d concurrent final state not a serial equivalent: %s", it, seed, diff)
		}
		// 额外不变量：每刀预占不得超过寿命上限（同一份剩余寿命绝不预占两次）。
		snap, _ := svc.Query("g")
		for _, ts := range snap.Order {
			if ts.Reserved > limit {
				t.Fatalf("iter=%d seed=%d over-reserved on %s: %d", it, seed, ts.ID, ts.Reserved)
			}
		}
	}
}

// TestDifferentialLoggedCase 打印一条随机计划的输入、输出与判定依据，
// 供人工审计「日志中打印输入、输出与判定依据」的要求（-v 可见）。
func TestDifferentialLoggedCase(t *testing.T) {
	seed := int64(99)
	plan := genPlan(rand.New(rand.NewSource(seed)), 40, 3)
	m := naive.New("g", naive.Cfg{Limit: 50, Warn: 600, Mode: naive.Strict}, toolIDs(3))
	svc := newSvc(t, "g",
		toollife.GroupConfig{Basis: toollife.BasisPieces, LifeLimit: 50, WarnPermille: 600, Mode: toollife.ModeStrict},
		toolIDs(3)...)
	t.Logf("=== differential logged case seed=%d limit=50 warn=600 strict ===", seed)
	for i, op := range plan {
		n := runNaive(m, op)
		ok, code, tid := runProd(svc, op)
		t.Logf("step=%02d ch=%d in=%s(group=%s tool=%s new=%s req=%s amt=%d) -> naive{ok=%v code=%s pick=%s} prod{ok=%v code=%s pick=%s}",
			i, op.ch, kindName(op.kind), op.group, op.tool, op.newTool, op.req, op.amount,
			n.ok, dash(n.code), dash(n.toolID), ok, dash(code), dash(tid))
		if (prodOutcome{ok: ok, code: code, toolID: tid}) != n {
			t.Fatalf("divergence at step %d", i)
		}
	}
	if diff := stateMismatch(t, svc, m); diff != "" {
		t.Fatalf("logged case mismatch: %s", diff)
	}
	snap, _ := svc.Query("g")
	for _, ts := range snap.Order {
		t.Logf("final tool=%s status=%s used=%d reserved=%d remaining=%d", ts.ID, ts.Status, ts.Used, ts.Reserved, ts.Remaining)
	}
	t.Logf("warnings=%+v", svc.Warnings())
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func kindName(k naive.ReqKind) string {
	switch k {
	case naive.KApply:
		return "apply"
	case naive.KSettle:
		return "settle"
	case naive.KAbort:
		return "abort"
	case naive.KBroken:
		return "broken"
	case naive.KReplace:
		return "replace"
	case naive.KLock:
		return "lock"
	case naive.KUnlock:
		return "unlock"
	}
	return "?"
}

func toolIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = toolID(i)
	}
	return out
}
