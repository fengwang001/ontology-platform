package signal_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/signal"
	"ontology/signal/naive"
)

// naiveKind 从朴素模型的错误文本还原错误类别。
func naiveKind(err error) signal.ErrKind {
	if err == nil {
		return 0
	}
	msg := err.Error()
	for k := signal.ErrInvalidParam; k <= signal.ErrTargetOccupied; k++ {
		if strings.HasPrefix(msg, k.String()+":") {
			return k
		}
	}
	return -1
}

func randPhases(rng *rand.Rand) []signal.Phase {
	n := 2 + rng.Intn(3)
	ph := make([]signal.Phase, n)
	for i := range ph {
		mn := 3 + rng.Intn(4)
		ph[i] = signal.Phase{MinGreen: mn, MaxGreen: mn + 2 + rng.Intn(7), Clear: 1 + rng.Intn(2)}
	}
	return ph
}

func randPlan(rng *rand.Rand, ph []signal.Phase, id string, feasible bool) signal.Plan {
	greens := make([]int, len(ph))
	var cycle int64
	for i, p := range ph {
		g := p.MinGreen + rng.Intn(p.MaxGreen-p.MinGreen+1)
		if !feasible && rng.Intn(2) == 0 {
			if rng.Intn(2) == 0 {
				g = p.MinGreen - 1
			} else {
				g = p.MaxGreen + 1
			}
		}
		greens[i] = g
		cycle += int64(g) + int64(p.Clear)
	}
	p := signal.Plan{ID: id, Greens: greens, MaxAdjust: int64(rng.Intn(4))}
	if cycle > 0 {
		p.Offset = rng.Int63n(cycle)
		if !feasible && rng.Intn(3) == 0 {
			p.Offset = cycle + rng.Int63n(5)
		}
	}
	return p
}

// TestRandomOpsAgainstNaive 随机操作序列下，事件驱动实现必须与
// 独立编写的逐秒朴素模型完全一致；日志记录每条操作的输入、输出与判定依据。
func TestRandomOpsAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runComparison(t, seed)
		})
	}
}

func runComparison(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	ph := randPhases(rng)
	plan0 := randPlan(rng, ph, "P0", true)
	eng, err := signal.NewController(ph, plan0)
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}
	nav, err := naive.NewController(ph, plan0)
	if err != nil {
		t.Fatalf("naive init: %v", err)
	}
	t.Logf("setup phases=%+v plan0=%+v", ph, plan0)

	var emIDs, busIDs, planIDs []string
	planIDs = append(planIDs, "P0")
	now := int64(0)
	step := 0
	log := func(input, engOut, navOut, reason string) {
		t.Logf("#%03d t=%d %-28s | engine=%-22s naive=%-22s | %s", step, now, input, engOut, navOut, reason)
	}
	fail := func(input string, engErr, navErr error) {
		t.Fatalf("step %d t=%d %s: engine err=%v naive err=%v", step, now, input, engErr, navErr)
	}

	for step = 0; step < 300; step++ {
		now += int64(rng.Intn(7))
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11: // 紧急请求
			id := fmt.Sprintf("E%d", rng.Intn(40))
			target := rng.Intn(len(ph) + 1)
			in := fmt.Sprintf("emergency %s target=%d", id, target)
			e1 := eng.RequestEmergency(id, target, now)
			e2 := nav.RequestEmergency(id, target, now)
			if signal.KindOf(e1) != naiveKind(e2) {
				fail(in, e1, e2)
			}
			reason := "accepted: queued by (time,id)"
			if e1 != nil {
				reason = "rejected: " + signal.KindOf(e1).String()
			} else {
				emIDs = append(emIDs, id)
			}
			log(in, errStr(e1), errStr(e2), reason)
		case 12, 13, 14, 15, 16, 17, 18: // 公交请求
			id := fmt.Sprintf("B%d", rng.Intn(25))
			mode := signal.BusExtend
			if rng.Intn(2) == 0 {
				mode = signal.BusShorten
			}
			amount := 1 + rng.Intn(5)
			in := fmt.Sprintf("bus %s mode=%d amount=%d", id, mode, amount)
			e1 := eng.RequestBus(id, mode, amount, now)
			e2 := nav.RequestBus(id, mode, amount, now)
			if signal.KindOf(e1) != naiveKind(e2) {
				fail(in, e1, e2)
			}
			reason := "accepted: applied to current phase"
			if e1 != nil {
				reason = "rejected: " + signal.KindOf(e1).String()
			} else {
				busIDs = append(busIDs, id)
				s1, _ := eng.BusStatus(id)
				s2, _ := nav.BusStatus(id)
				if s1 != s2 {
					t.Fatalf("step %d bus %s status: engine=%s naive=%s", step, id, s1, s2)
				}
				reason = "accepted: final=" + string(s1)
			}
			log(in, errStr(e1), errStr(e2), reason)
		case 19, 20, 21, 22: // 通过确认
			id := fmt.Sprintf("E%d", rng.Intn(40))
			in := fmt.Sprintf("confirm %s", id)
			e1 := eng.ConfirmPass(id, now)
			e2 := nav.ConfirmPass(id, now)
			if signal.KindOf(e1) != naiveKind(e2) {
				fail(in, e1, e2)
			}
			reason := "accepted: hold ends early (>= min green)"
			if e1 != nil {
				reason = "rejected: " + signal.KindOf(e1).String()
			}
			log(in, errStr(e1), errStr(e2), reason)
		case 23, 24, 25, 26, 27, 28: // 方案变更
			id := fmt.Sprintf("P%d", len(planIDs))
			p := randPlan(rng, ph, id, rng.Intn(4) != 0)
			in := fmt.Sprintf("plan %s greens=%v off=%d adj=%d", id, p.Greens, p.Offset, p.MaxAdjust)
			e1 := eng.ChangePlan(p, now)
			e2 := nav.ChangePlan(p, now)
			if signal.KindOf(e1) != naiveKind(e2) {
				fail(in, e1, e2)
			}
			reason := "accepted: pending until cycle end"
			if e1 != nil {
				reason = "rejected: " + signal.KindOf(e1).String()
			} else {
				planIDs = append(planIDs, id)
			}
			log(in, errStr(e1), errStr(e2), reason)
		default: // 查询
			in := "query"
			q1, e1 := eng.Query(now)
			q2, e2 := nav.Query(now)
			if signal.KindOf(e1) != naiveKind(e2) {
				fail(in, e1, e2)
			}
			if e1 == nil {
				if q1 != q2 {
					t.Fatalf("step %d t=%d query diverged:\nengine=%+v\nnaive =%+v", step, now, q1, q2)
				}
				log(in, fmt.Sprintf("ph%d rem%d dev%d", q1.Phase, q1.Remaining, q1.Deviation),
					fmt.Sprintf("ph%d rem%d dev%d", q2.Phase, q2.Remaining, q2.Deviation),
					"snapshot identical (event-driven == per-second)")
			} else {
				log(in, errStr(e1), errStr(e2), "rejected: "+signal.KindOf(e1).String())
			}
		}
	}

	// 终态对比：所有请求与方案状态。
	for _, id := range emIDs {
		s1, _ := eng.EmergencyStatus(id)
		s2, _ := nav.EmergencyStatus(id)
		if s1 != s2 {
			t.Fatalf("emergency %s final: engine=%s naive=%s", id, s1, s2)
		}
		t.Logf("final emergency %-6s engine=%-10s naive=%-10s | match", id, s1, s2)
	}
	for _, id := range busIDs {
		s1, _ := eng.BusStatus(id)
		s2, _ := nav.BusStatus(id)
		if s1 != s2 {
			t.Fatalf("bus %s final: engine=%s naive=%s", id, s1, s2)
		}
		t.Logf("final bus      %-6s engine=%-10s naive=%-10s | match", id, s1, s2)
	}
	for _, id := range planIDs {
		s1, _ := eng.PlanStatus(id)
		s2, _ := nav.PlanStatus(id)
		if s1 != s2 {
			t.Fatalf("plan %s final: engine=%s naive=%s", id, s1, s2)
		}
	}
	// 远距离未来查询：引擎 O(1) 快跳，朴素模型逐秒推进，结果必须一致。
	far := now + 2_000_000
	q1, e1 := eng.Query(far)
	q2, e2 := nav.Query(far)
	if (e1 != nil) != (e2 != nil) || q1 != q2 {
		t.Fatalf("far query t=%d diverged: %+v(%v) vs %+v(%v)", far, q1, e1, q2, e2)
	}
	t.Logf("far query t=%d engine=%+v naive=%+v | match (engine O(1) fast path)", far, q1, q2)
}

func errStr(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}
