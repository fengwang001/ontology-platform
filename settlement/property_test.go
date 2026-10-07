package settlement_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/settlement"
)

// testOp 描述一个可重放的操作。
type testOp struct {
	kind       string // accept / release / regdef / closedef / change / terminate
	contractID string
	milestone  string
	defectID   string
	passed     bool
	penalty    int64
	now        int64
	effDay     int64
	adjs       []settlement.Adjustment
}

func (o testOp) String() string {
	switch o.kind {
	case "accept":
		return fmt.Sprintf("Accept(c=%s m=%s passed=%v now=%d)", o.contractID, o.milestone, o.passed, o.now)
	case "release":
		return fmt.Sprintf("ReleaseRetention(c=%s m=%s now=%d)", o.contractID, o.milestone, o.now)
	case "regdef":
		return fmt.Sprintf("RegisterDefect(c=%s m=%s d=%s penalty=%d now=%d)", o.contractID, o.milestone, o.defectID, o.penalty, o.now)
	case "closedef":
		return fmt.Sprintf("CloseDefect(c=%s m=%s d=%s now=%d)", o.contractID, o.milestone, o.defectID, o.now)
	case "change":
		return fmt.Sprintf("ChangeOrder(c=%s eff=%d adjs=%v now=%d)", o.contractID, o.effDay, o.adjs, o.now)
	case "terminate":
		return fmt.Sprintf("Terminate(c=%s now=%d)", o.contractID, o.now)
	default:
		return "unknown"
	}
}

// genContracts 生成两个随机合同参数。
func genContracts(r *rand.Rand) []settlement.ContractParams {
	var out []settlement.ContractParams
	for i := 0; i < 2; i++ {
		p := settlement.ContractParams{
			ID:               fmt.Sprintf("c%d", i),
			TotalAmount:      100000,
			AdvanceTotal:     r.Int63n(40000),
			AdvanceRatio:     r.Int63n(6001),
			RetentionRatio:   r.Int63n(2001),
			RetentionDays:    r.Int63n(20),
			PenaltyDailyRate: r.Int63n(501),
			PenaltyCapRatio:  r.Int63n(3001),
		}
		for j := 0; j < 4; j++ {
			p.Milestones = append(p.Milestones, settlement.MilestoneParams{
				ID:      fmt.Sprintf("m%d", j),
				Payable: 10000 + r.Int63n(10001),
				PlanDay: r.Int63n(30),
			})
		}
		out = append(out, p)
	}
	return out
}

// genOps 生成确定性（由 seed 决定）的随机操作序列，无需服务反馈。
func genOps(r *rand.Rand, contracts []settlement.ContractParams, n int) []testOp {
	var ops []testOp
	var genNow int64
	defectSeq := 0
	knownDefects := []struct{ c, m, d string }{}

	pickContract := func() settlement.ContractParams { return contracts[r.Intn(len(contracts))] }
	pickMilestone := func(c settlement.ContractParams) string {
		if r.Intn(20) == 0 {
			return "bogus" // 5% 不存在的里程碑
		}
		return c.Milestones[r.Intn(len(c.Milestones))].ID
	}
	nextNow := func() int64 {
		switch r.Intn(10) {
		case 0: // 10% 时钟回退
			back := genNow - 1 - r.Int63n(3)
			if back < 0 {
				back = 0
			}
			return back
		case 1: // 10% 原地
			return genNow
		default:
			genNow += r.Int63n(4)
			return genNow
		}
	}

	for i := 0; i < n; i++ {
		c := pickContract()
		switch r.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
			20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34: // 35% 验收
			ops = append(ops, testOp{
				kind: "accept", contractID: c.ID, milestone: pickMilestone(c),
				passed: r.Intn(4) != 0, now: nextNow(),
			})
		case 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49: // 15% 释放
			ops = append(ops, testOp{kind: "release", contractID: c.ID, milestone: pickMilestone(c), now: nextNow()})
		case 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61: // 12% 登记缺陷
			did := fmt.Sprintf("d%d", defectSeq)
			defectSeq++
			if r.Intn(10) == 0 && len(knownDefects) > 0 { // 10% 复用旧缺陷 ID
				did = knownDefects[r.Intn(len(knownDefects))].d
			}
			mid := pickMilestone(c)
			knownDefects = append(knownDefects, struct{ c, m, d string }{c.ID, mid, did})
			ops = append(ops, testOp{
				kind: "regdef", contractID: c.ID, milestone: mid, defectID: did,
				penalty: r.Int63n(30000), now: nextNow(),
			})
		case 62, 63, 64, 65, 66, 67, 68, 69: // 8% 关闭缺陷
			did := "bogus"
			mid := pickMilestone(c)
			if len(knownDefects) > 0 && r.Intn(10) < 8 {
				kd := knownDefects[r.Intn(len(knownDefects))]
				did, mid = kd.d, kd.m
			}
			ops = append(ops, testOp{kind: "closedef", contractID: c.ID, milestone: mid, defectID: did, now: nextNow()})
		case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81: // 12% 变更
			now := nextNow()
			nadj := 1 + r.Intn(2)
			var adjs []settlement.Adjustment
			used := map[string]bool{}
			for k := 0; k < nadj; k++ {
				mid := pickMilestone(c)
				if used[mid] {
					continue
				}
				used[mid] = true
				payable := r.Int63n(40000)
				if r.Intn(15) == 0 {
					payable = 90000 + r.Int63n(50000) // 偶发超额变更
				}
				adjs = append(adjs, settlement.Adjustment{
					MilestoneID: mid, Payable: payable, PlanDay: r.Int63n(60),
				})
			}
			if len(adjs) == 0 {
				continue
			}
			ops = append(ops, testOp{kind: "change", contractID: c.ID, effDay: now + r.Int63n(3), adjs: adjs, now: now})
		case 82, 83, 84, 85: // 4% 终止
			ops = append(ops, testOp{kind: "terminate", contractID: c.ID, now: nextNow()})
		default: // 14% 非法操作
			switch r.Intn(4) {
			case 0:
				ops = append(ops, testOp{kind: "accept", contractID: "", milestone: "m0", passed: true, now: nextNow()})
			case 1:
				ops = append(ops, testOp{kind: "accept", contractID: "bogus", milestone: "m0", passed: true, now: nextNow()})
			case 2:
				ops = append(ops, testOp{kind: "regdef", contractID: c.ID, milestone: pickMilestone(c), defectID: "dx", penalty: -1, now: nextNow()})
			case 3:
				ops = append(ops, testOp{kind: "release", contractID: c.ID, milestone: "", now: nextNow()})
			}
		}
	}
	return ops
}

// stepOutcome 记录一步操作在两侧的结果，用于比较与日志。
type stepOutcome struct {
	svcErr  error
	svcRes  any // *settlement.SettlementResult 或 *settlement.ReleaseResult 或 nil
	mdlErr  *settlement.Error
	mdlRes  any
	mdlNote string
	svcSum  settlement.Summary
	mdlSum  settlement.Summary
}

// runSequence 在服务与模型上并行执行操作序列，逐步比较并打印日志。
func runSequence(t *testing.T, seed int64, contracts []settlement.ContractParams, ops []testOp) {
	t.Helper()
	svc := settlement.NewService()
	mdl := newModel()

	for i, p := range contracts {
		if err := svc.CreateContract(p, 0); err != nil {
			t.Fatalf("seed=%d create %s: %v", seed, p.ID, err)
		}
		if err := mdl.createContract(p, 0); err != nil {
			t.Fatalf("seed=%d model create %s: %v", seed, p.ID, err)
		}
		t.Logf("seed=%d init: CreateContract(%s) total=%d advance=%d/%d retention=%d/%dd penalty=%d/%d cap milestones=%d",
			seed, p.ID, p.TotalAmount, p.AdvanceTotal, p.AdvanceRatio, p.RetentionRatio, p.RetentionDays,
			p.PenaltyDailyRate, p.PenaltyCapRatio, len(p.Milestones))
		_ = i
	}

	for step, op := range ops {
		out := applyBoth(svc, mdl, op)

		// 比较错误类别。
		svcKind, svcOK := kindOf(out.svcErr)
		mdlKind, mdlOK := kindOf(out.mdlErr)
		match := svcOK == mdlOK && svcKind == mdlKind && resultsEqual(out.svcRes, out.mdlRes)

		t.Logf("seed=%d step=%03d in=%s | svc=[%s %s] model=[%s %s] 依据=%s 判定=%v",
			seed, step, op, svcKind, briefRes(out.svcRes), mdlKind, briefRes(out.mdlRes), out.mdlNote, match)

		if !match {
			t.Fatalf("seed=%d step=%d divergence on %s:\n svc: err=%v res=%+v\nmodel: err=%v res=%+v",
				seed, step, op, out.svcErr, out.svcRes, out.mdlErr, out.mdlRes)
		}

		// 每步比较汇总并校验守恒。
		for _, p := range contracts {
			ss, err := svc.Summary(p.ID)
			if err != nil {
				t.Fatalf("seed=%d step=%d summary %s: %v", seed, step, p.ID, err)
			}
			ms, merr := mdl.summary(p.ID)
			if merr != nil {
				t.Fatalf("seed=%d step=%d model summary %s: %v", seed, step, p.ID, merr)
			}
			if ss != ms {
				t.Fatalf("seed=%d step=%d summary divergence %s:\n svc=%+v\nmodel=%+v", seed, step, p.ID, ss, ms)
			}
			if !ss.Conserved() {
				t.Fatalf("seed=%d step=%d conservation violated %s: %+v", seed, step, p.ID, ss)
			}
			if ss.PenaltyCharged > ss.PenaltyCap {
				t.Fatalf("seed=%d step=%d penalty over cap %s: %+v", seed, step, p.ID, ss)
			}
			if ss.AdvanceDeducted > p.AdvanceTotal {
				t.Fatalf("seed=%d step=%d advance over total %s: %+v", seed, step, p.ID, ss)
			}
		}
	}
}

func applyBoth(svc *settlement.Service, mdl *model, op testOp) stepOutcome {
	var out stepOutcome
	switch op.kind {
	case "accept":
		var r *settlement.SettlementResult
		r, out.svcErr = svc.Accept(op.contractID, op.milestone, op.passed, op.now)
		out.svcRes = r
		var mr *settlement.SettlementResult
		mr, out.mdlErr, out.mdlNote = mdl.accept(op.contractID, op.milestone, op.passed, op.now)
		out.mdlRes = mr
	case "release":
		var r *settlement.ReleaseResult
		r, out.svcErr = svc.ReleaseRetention(op.contractID, op.milestone, op.now)
		out.svcRes = r
		var mr *settlement.ReleaseResult
		mr, out.mdlErr, out.mdlNote = mdl.release(op.contractID, op.milestone, op.now)
		out.mdlRes = mr
	case "regdef":
		out.svcErr = svc.RegisterDefect(op.contractID, op.milestone, op.defectID, op.penalty, op.now)
		out.mdlErr, out.mdlNote = mdl.registerDefect(op.contractID, op.milestone, op.defectID, op.penalty, op.now)
	case "closedef":
		out.svcErr = svc.CloseDefect(op.contractID, op.milestone, op.defectID, op.now)
		out.mdlErr, out.mdlNote = mdl.closeDefect(op.contractID, op.milestone, op.defectID, op.now)
	case "change":
		out.svcErr = svc.ChangeOrder(op.contractID, op.effDay, op.adjs, op.now)
		out.mdlErr, out.mdlNote = mdl.changeOrder(op.contractID, op.effDay, op.adjs, op.now)
	case "terminate":
		out.svcErr = svc.Terminate(op.contractID, op.now)
		out.mdlErr, out.mdlNote = mdl.terminate(op.contractID, op.now)
	}
	return out
}

func kindOf(err error) (string, bool) {
	if err == nil {
		return "ok", true
	}
	var se *settlement.Error
	if !errorAs(err, &se) {
		return fmt.Sprintf("non-settlement-error(%v)", err), false
	}
	if se == nil { // 类型化 nil（模型侧无错误）
		return "ok", true
	}
	return se.Kind.String(), false
}

func errorAs(err error, target **settlement.Error) bool {
	for err != nil {
		if se, ok := err.(*settlement.Error); ok {
			*target = se
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func resultsEqual(a, b any) bool {
	switch av := a.(type) {
	case *settlement.SettlementResult:
		bv, ok := b.(*settlement.SettlementResult)
		if !ok {
			return false
		}
		if av == nil || bv == nil {
			return av == nil && bv == nil
		}
		return *av == *bv
	case *settlement.ReleaseResult:
		bv, ok := b.(*settlement.ReleaseResult)
		if !ok {
			return false
		}
		if av == nil || bv == nil {
			return av == nil && bv == nil
		}
		return *av == *bv
	default:
		return a == nil && b == nil
	}
}

func briefRes(res any) string {
	switch v := res.(type) {
	case *settlement.SettlementResult:
		if v == nil {
			return "-"
		}
		return fmt.Sprintf("{P=%d R=%d A=%d arrP=%d due=%d penP=%d paid=%d arrL=%d}",
			v.Payable, v.Retention, v.AdvanceDeduct, v.ArrearsPaid, v.PenaltyDue, v.PenaltyPaid, v.Paid, v.ArrearsLeft)
	case *settlement.ReleaseResult:
		if v == nil {
			return "-"
		}
		return fmt.Sprintf("{ret=%d ded=%d paid=%d}", v.Retention, v.DefectDeduct, v.Paid)
	default:
		return "-"
	}
}

// TestPropertyAgainstNaiveModel 与朴素模型对照大量随机操作序列。
func TestPropertyAgainstNaiveModel(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			contracts := genContracts(r)
			ops := genOps(r, contracts, 300)
			runSequence(t, seed, contracts, ops)
		})
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(20261007))
	contracts := genContracts(r)
	ops := genOps(r, contracts, 500)

	run := func() []settlement.Summary {
		svc := settlement.NewService()
		for _, p := range contracts {
			if err := svc.CreateContract(p, 0); err != nil {
				t.Fatalf("create: %v", err)
			}
		}
		for _, op := range ops {
			applySvcOnly(svc, op)
		}
		var sums []settlement.Summary
		for _, p := range contracts {
			s, err := svc.Summary(p.ID)
			if err != nil {
				t.Fatalf("summary: %v", err)
			}
			sums = append(sums, s)
		}
		return sums
	}

	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("summary count mismatch")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay divergence on contract %d:\nfirst=%+v\nsecond=%+v", i, first[i], second[i])
		}
	}
}

func applySvcOnly(svc *settlement.Service, op testOp) {
	switch op.kind {
	case "accept":
		_, _ = svc.Accept(op.contractID, op.milestone, op.passed, op.now)
	case "release":
		_, _ = svc.ReleaseRetention(op.contractID, op.milestone, op.now)
	case "regdef":
		_ = svc.RegisterDefect(op.contractID, op.milestone, op.defectID, op.penalty, op.now)
	case "closedef":
		_ = svc.CloseDefect(op.contractID, op.milestone, op.defectID, op.now)
	case "change":
		_ = svc.ChangeOrder(op.contractID, op.effDay, op.adjs, op.now)
	case "terminate":
		_ = svc.Terminate(op.contractID, op.now)
	}
}
