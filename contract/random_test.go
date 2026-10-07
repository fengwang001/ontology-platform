package contract

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 随机操作序列差分测试：优化实现（物化时间线 + 惰性续签游标）
// 与独立编写的朴素模型（全量扫描重推演）逐步对照，
// 日志打印每步输入、输出与判定依据；末尾再做整序列重放校验。

type genState struct {
	contracts  []int
	amendments map[int]int
	signs      map[int]map[int][]string // 合同 -> 协议 -> 已生成过签署操作的参与方
	now        int
}

func randomParams(rng *rand.Rand, now int) ContractParams {
	start := now + rng.Intn(2)
	auto := 1
	if rng.Intn(8) == 0 {
		auto = 0
	}
	clauses := map[int]int{
		0: rng.Intn(100), 1: rng.Intn(100), 2: rng.Intn(100),
		3: auto, 4: 3 + rng.Intn(5), 5: rng.Intn(3),
	}
	locked := []int{2}
	if rng.Intn(2) == 0 {
		locked = append(locked, 1)
	}
	return ContractParams{
		Parties:         [2]string{"A", "B"},
		Clauses:         clauses,
		Locked:          locked,
		Start:           start,
		Expiry:          start + 6 + rng.Intn(10),
		AutoRenewClause: 3,
		PeriodClause:    4,
		NoticeClause:    5,
		SignWindowDays:  2 + rng.Intn(4),
	}
}

func describeOp(op Op) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s now=%d c=%d", op.Kind, op.Now, op.Contract)
	switch op.Kind {
	case OpCreateContract:
		p := op.Params
		fmt.Fprintf(&b, " start=%d expiry=%d auto=%d period=%d notice=%d win=%d locked=%v",
			p.Start, p.Expiry, p.Clauses[3], p.Clauses[4], p.Clauses[5], p.SignWindowDays, p.Locked)
	case OpCreateAmendment:
		if op.IsRevocation {
			fmt.Fprintf(&b, " revoke=%d declared=%d", op.RevokeTarget, op.DeclaredEffDay)
		} else {
			fmt.Fprintf(&b, " mods=%v declared=%d", op.Mods, op.DeclaredEffDay)
		}
	case OpSign:
		fmt.Fprintf(&b, " a=%d party=%s auth=[%d,%d]", op.Amendment, op.Party, op.AuthFrom, op.AuthTo)
	case OpCountersign:
		fmt.Fprintf(&b, " a=%d", op.Amendment)
	case OpNotice:
		fmt.Fprintf(&b, " party=%s", op.Party)
	case OpQueryValue:
		fmt.Fprintf(&b, " clause=%d day=%d", op.Clause, op.Day)
	case OpQueryInForce:
		fmt.Fprintf(&b, " day=%d", op.Day)
	}
	return b.String()
}

func describeResult(res OpResult) string {
	if res.Err != None {
		return fmt.Sprintf("err=%s", res.Err)
	}
	switch {
	case res.Source.Master || res.Source.AmendmentID >= 0 && res.Value != 0:
		return fmt.Sprintf("ok value=%d src=%s", res.Value, res.Source)
	default:
		return fmt.Sprintf("ok%+v", res)
	}
}

func equalRenewals(a, b []Renewal) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func compareResults(t *testing.T, step int, op Op, got, want OpResult) {
	t.Helper()
	if got.Err != want.Err {
		t.Fatalf("step %d [%s]: err %s != %s", step, describeOp(op), got.Err, want.Err)
	}
	if got.Err != None {
		return
	}
	if got.ContractID != want.ContractID || got.AmendmentID != want.AmendmentID ||
		got.Value != want.Value || got.Source != want.Source ||
		got.InForce != want.InForce || got.Expiry != want.Expiry ||
		!equalRenewals(got.Renewals, want.Renewals) {
		t.Fatalf("step %d [%s]: got %+v, want %+v", step, describeOp(op), got, want)
	}
}

func genOp(rng *rand.Rand, gs *genState, nv *naive) Op {
	now := gs.now
	// 没有存活合同（或还没有合同）时优先创建新合同；否则小概率新建
	anyActive := false
	for _, cid := range gs.contracts {
		if c := nv.contracts[cid]; c != nil && nv.active(c, now) {
			anyActive = true
			break
		}
	}
	if !anyActive || (len(gs.contracts) < 6 && rng.Intn(100) < 3) {
		return Op{Kind: OpCreateContract, Now: now, Params: randomParams(rng, now)}
	}
	cid := gs.contracts[rng.Intn(len(gs.contracts))]
	// 偏向选择存活合同，但保留小概率命中已终止/届满合同以覆盖状态拒绝
	if c := nv.contracts[cid]; c != nil && !nv.active(c, now) && rng.Intn(100) < 85 {
		for _, alt := range gs.contracts {
			if ac := nv.contracts[alt]; ac != nil && nv.active(ac, now) {
				cid = alt
				break
			}
		}
	}
	na := gs.amendments[cid]
	if na == 0 {
		return genAmendment(rng, gs, cid)
	}
	roll := rng.Intn(100)
	switch {
	case roll < 24: // 创建协议
		return genAmendment(rng, gs, cid)
	case roll < 52: // 签署：偏向选择尚未签过的参与方以促成签署完成
		aid := rng.Intn(na)
		party := "A"
		signed := gs.signs[cid][aid]
		if len(signed) == 1 {
			if signed[0] == "A" {
				party = "B"
			}
		} else if rng.Intn(2) == 0 {
			party = "B"
		}
		var from, to int
		switch rng.Intn(10) {
		case 0:
			from, to = now+1, now // 倒置窗口（参数非法）
		case 1:
			from, to = now+1, now+5 // 尚未进入授权期
		case 2:
			from, to = 0, max(0, now-1) // 已过授权期
		default:
			from, to = max(0, now-2), now+3
		}
		gs.signs[cid][aid] = append(gs.signs[cid][aid], party)
		return Op{Kind: OpSign, Now: now, Contract: cid, Amendment: aid, Party: party, AuthFrom: from, AuthTo: to}
	case roll < 58: // 会签
		return Op{Kind: OpCountersign, Now: now, Contract: cid, Amendment: rng.Intn(na)}
	case roll < 62: // 不续签通知
		party := "A"
		if rng.Intn(2) == 0 {
			party = "B"
		}
		return Op{Kind: OpNotice, Now: now, Contract: cid, Party: party}
	case roll < 64: // 提前终止
		return Op{Kind: OpTerminate, Now: now, Contract: cid}
	case roll < 82: // 查询有效值
		day := now - rng.Intn(25)
		if rng.Intn(4) == 0 {
			day = now + rng.Intn(4)
		}
		if day < 0 {
			day = 0
		}
		return Op{Kind: OpQueryValue, Now: now, Contract: cid, Clause: rng.Intn(6), Day: day}
	case roll < 88: // 查询是否在期
		day := now - rng.Intn(25)
		if rng.Intn(3) == 0 {
			day = now + rng.Intn(10)
		}
		if day < 0 {
			day = 0
		}
		return Op{Kind: OpQueryInForce, Now: now, Contract: cid, Day: day}
	case roll < 92: // 查询当前到期日
		return Op{Kind: OpQueryExpiry, Now: now, Contract: cid}
	case roll < 96: // 查询续签记录
		return Op{Kind: OpQueryRenewals, Now: now, Contract: cid}
	default: // 各类非法操作
		switch rng.Intn(5) {
		case 0:
			return Op{Kind: OpSign, Now: now, Contract: cid + 100, Amendment: 0, Party: "A", AuthFrom: 0, AuthTo: 100}
		case 1:
			return Op{Kind: OpSign, Now: now, Contract: cid, Amendment: na + 100, Party: "A", AuthFrom: 0, AuthTo: 100}
		case 2:
			return Op{Kind: OpQueryValue, Now: now, Contract: cid, Clause: 0, Day: -1}
		case 3:
			return Op{Kind: OpNotice, Now: now, Contract: cid, Party: ""}
		default:
			if now > 0 {
				return Op{Kind: OpQueryInForce, Now: now - 1, Contract: cid, Day: now}
			}
			return Op{Kind: OpQueryExpiry, Now: now, Contract: cid}
		}
	}
}

func genAmendment(rng *rand.Rand, gs *genState, cid int) Op {
	now := gs.now
	na := gs.amendments[cid]
	declared := now + rng.Intn(8) - 2
	if declared < 0 {
		declared = 0
	}
	if na > 0 && rng.Intn(100) < 20 {
		return Op{Kind: OpCreateAmendment, Now: now, Contract: cid,
			DeclaredEffDay: declared, IsRevocation: true, RevokeTarget: rng.Intn(na)}
	}
	mods := map[int]int{}
	for _, clause := range rng.Perm(6)[:1+rng.Intn(2)] {
		mods[clause] = rng.Intn(6)
	}
	return Op{Kind: OpCreateAmendment, Now: now, Contract: cid, DeclaredEffDay: declared, Mods: mods}
}

func runRandomSequence(t *testing.T, seed int64, steps int) {
	rng := rand.New(rand.NewSource(seed))
	svc := NewService()
	nv := newNaive()
	gs := &genState{amendments: map[int]int{}, signs: map[int]map[int][]string{}}

	for step := 0; step < steps; step++ {
		op := genOp(rng, gs, nv)
		got := svc.Apply(op)
		want, reason := nv.apply(op)

		if op.Kind == OpCreateContract && got.Err == None {
			gs.contracts = append(gs.contracts, got.ContractID)
			gs.signs[got.ContractID] = map[int][]string{}
		}
		if op.Kind == OpCreateAmendment && got.Err == None {
			gs.amendments[op.Contract]++
		}

		t.Logf("step %04d | %s => %s | 依据: %s", step, describeOp(op), describeResult(got), reason)
		compareResults(t, step, op, got, want)

		// 推进时钟：多数原地或小步，偶发大步
		if r := rng.Intn(100); r < 60 {
			gs.now += rng.Intn(2)
		} else if r < 68 {
			gs.now += 2 + rng.Intn(6)
		}
	}

	// 收尾全量扫描：每个合同随机抽查大量 (条款, 日期) 与在期判定
	sort.Ints(gs.contracts)
	for _, cid := range gs.contracts {
		for i := 0; i < 30; i++ {
			op := Op{Kind: OpQueryValue, Now: gs.now, Contract: cid, Clause: rng.Intn(6), Day: rng.Intn(gs.now + 5)}
			got := svc.Apply(op)
			want, reason := nv.apply(op)
			t.Logf("sweep | %s => %s | 依据: %s", describeOp(op), describeResult(got), reason)
			compareResults(t, -1, op, got, want)
		}
		for i := 0; i < 15; i++ {
			op := Op{Kind: OpQueryInForce, Now: gs.now, Contract: cid, Day: rng.Intn(gs.now + 10)}
			compareResults(t, -1, op, svc.Apply(op), mustNaive(t, nv, op))
		}
		compareResults(t, -1, Op{Kind: OpQueryExpiry, Now: gs.now, Contract: cid},
			svc.Apply(Op{Kind: OpQueryExpiry, Now: gs.now, Contract: cid}),
			mustNaive(t, nv, Op{Kind: OpQueryExpiry, Now: gs.now, Contract: cid}))
		compareResults(t, -1, Op{Kind: OpQueryRenewals, Now: gs.now, Contract: cid},
			svc.Apply(Op{Kind: OpQueryRenewals, Now: gs.now, Contract: cid}),
			mustNaive(t, nv, Op{Kind: OpQueryRenewals, Now: gs.now, Contract: cid}))
	}

	// 重放校验：相同操作序列在全新服务上重放，结果须完全一致
	log := svc.Log()
	ops := make([]Op, len(log))
	for i, e := range log {
		ops[i] = e.Op
	}
	replayed := Replay(ops)
	for i := range log {
		got, want := replayed[i], log[i].Result
		if got.Err != want.Err || got.ContractID != want.ContractID || got.AmendmentID != want.AmendmentID ||
			got.Value != want.Value || got.Source != want.Source || got.InForce != want.InForce ||
			got.Expiry != want.Expiry || !equalRenewals(got.Renewals, want.Renewals) {
			t.Fatalf("replay diverged at step %d [%s]: got %+v, want %+v", i, describeOp(ops[i]), got, want)
		}
	}
	t.Logf("seed=%d: %d steps + sweep + replay(%d ops) all consistent", seed, steps, len(ops))
}

func mustNaive(t *testing.T, nv *naive, op Op) OpResult {
	t.Helper()
	res, _ := nv.apply(op)
	return res
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 16; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomSequence(t, seed, 1500)
		})
	}
}
