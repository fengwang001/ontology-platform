package leave_test

import (
	"math/rand"
	"testing"

	"ontology/leave"
	"ontology/leave/naive"
)

func randomConfig(rng *rand.Rand) leave.Config {
	seen := make(map[int]bool)
	var nws []int
	for len(nws) < 20+rng.Intn(40) {
		if d := rng.Intn(365); !seen[d] {
			seen[d] = true
			nws = append(nws, d)
		}
	}
	bounds := []int{1 + rng.Intn(4)}
	if rng.Intn(2) == 0 {
		bounds = append(bounds, bounds[0]+1+rng.Intn(8))
	}
	quotas := make([]int, len(bounds)+1)
	for i := range quotas {
		quotas[i] = 5 + rng.Intn(25)
	}
	return leave.Config{
		NonWorkdays:   nws,
		TenureBounds:  bounds,
		AnnualQuotas:  quotas,
		CarryCap:      rng.Intn(7),
		CarryDeadline: rng.Intn(365),
	}
}

func catOf(err error) string {
	if err == nil {
		return "ok"
	}
	if c, ok := leave.CategoryOf(err); ok {
		return c.String()
	}
	return "foreign:" + err.Error()
}

func chargesEqual(a, b []leave.Charge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type issuedLeave struct {
	id       int64
	from, to int
}

// TestRandomModelComparison 用大量随机操作序列对照主实现与独立朴素模型，
// 逐步打印输入、输出与判定依据（逐日扣减来源 / 余额四量 / 错误类别）。
func TestRandomModelComparison(t *testing.T) {
	const trials = 40
	const steps = 400
	for trial := 0; trial < trials; trial++ {
		seed := int64(20261006 + trial)
		rng := rand.New(rand.NewSource(seed))
		cfg := randomConfig(rng)
		svc, err := leave.NewService(cfg)
		if err != nil {
			t.Fatalf("trial %d: bad config: %v", trial, err)
		}
		nsv := naive.NewService(cfg)

		emps := []string{"alice", "bob", "carol"}
		for i, e := range emps {
			hire := rng.Intn(30)
			if err := svc.Register(i, e, hire); err != nil {
				t.Fatalf("trial %d: register %s: %v", trial, e, err)
			}
			if err := nsv.Register(i, e, hire); err != nil {
				t.Fatalf("trial %d: naive register %s: %v", trial, e, err)
			}
		}

		issued := make(map[string][]issuedLeave)
		now := 3
		log := func(format string, args ...any) {
			t.Logf("trial=%d seed=%d "+format, append([]any{trial, seed}, args...)...)
		}
		fail := func(format string, args ...any) {
			t.Fatalf("trial=%d seed=%d "+format, append([]any{trial, seed}, args...)...)
		}

		pickLeave := func(emp string) (issuedLeave, bool) {
			ls := issued[emp]
			if len(ls) == 0 || rng.Intn(10) == 0 {
				// 偶尔使用不存在的假单 ID
				return issuedLeave{id: int64(rng.Intn(1000) + 100)}, false
			}
			return ls[rng.Intn(len(ls))], true
		}

		for step := 0; step < steps; step++ {
			now += rng.Intn(6)
			if rng.Intn(50) == 0 {
				now -= rng.Intn(10) // 时钟回退场景
				if now < 0 {
					now = 0
				}
			}
			emp := emps[rng.Intn(len(emps))]
			op := rng.Intn(100)

			switch {
			case op < 35: // 申请
				from := now + rng.Intn(4)
				if rng.Intn(10) == 0 {
					from = now - rng.Intn(3) // 偶尔 from < now 触发参数非法
				}
				to := from + rng.Intn(20)
				if rng.Intn(5) == 0 {
					to = from + rng.Intn(500) // 偶尔长假：跨年/额度不足
				}
				if rng.Intn(20) == 0 {
					to = from - 1 - rng.Intn(5)
				}
				id1, ch1, err1 := svc.RequestLeave(now, emp, from, to)
				id2, ch2, err2 := nsv.RequestLeave(now, emp, from, to)
				log("step=%d REQUEST emp=%s now=%d [%d,%d] -> main(id=%d charges=%v err=%v) naive(id=%d charges=%v err=%v)",
					step, emp, now, from, to, id1, ch1, err1, id2, ch2, err2)
				if catOf(err1) != catOf(err2) {
					fail("REQUEST [%d,%d] now=%d: main=%s naive=%s", from, to, now, catOf(err1), catOf(err2))
				}
				if err1 == nil {
					if id1 != id2 || !chargesEqual(ch1, ch2) {
						fail("REQUEST [%d,%d] now=%d: main(id=%d,%v) naive(id=%d,%v)", from, to, now, id1, ch1, id2, ch2)
					}
					issued[emp] = append(issued[emp], issuedLeave{id: id1, from: from, to: to})
				}

			case op < 50: // 批准
				l, _ := pickLeave(emp)
				err1 := svc.Approve(now, emp, l.id)
				err2 := nsv.Approve(now, emp, l.id)
				log("step=%d APPROVE emp=%s now=%d leave=%d -> main=%s naive=%s", step, emp, now, l.id, catOf(err1), catOf(err2))
				if catOf(err1) != catOf(err2) {
					fail("APPROVE leave=%d now=%d: main=%s naive=%s", l.id, now, catOf(err1), catOf(err2))
				}

			case op < 60: // 驳回
				l, _ := pickLeave(emp)
				err1 := svc.Reject(now, emp, l.id)
				err2 := nsv.Reject(now, emp, l.id)
				log("step=%d REJECT emp=%s now=%d leave=%d -> main=%s naive=%s", step, emp, now, l.id, catOf(err1), catOf(err2))
				if catOf(err1) != catOf(err2) {
					fail("REJECT leave=%d now=%d: main=%s naive=%s", l.id, now, catOf(err1), catOf(err2))
				}

			case op < 70: // 撤回
				l, _ := pickLeave(emp)
				err1 := svc.Withdraw(now, emp, l.id)
				err2 := nsv.Withdraw(now, emp, l.id)
				log("step=%d WITHDRAW emp=%s now=%d leave=%d -> main=%s naive=%s", step, emp, now, l.id, catOf(err1), catOf(err2))
				if catOf(err1) != catOf(err2) {
					fail("WITHDRAW leave=%d now=%d: main=%s naive=%s", l.id, now, catOf(err1), catOf(err2))
				}

			case op < 80: // 整单销假
				l, _ := pickLeave(emp)
				err1 := svc.CancelLeave(now, emp, l.id)
				err2 := nsv.CancelLeave(now, emp, l.id)
				log("step=%d CANCEL emp=%s now=%d leave=%d -> main=%s naive=%s", step, emp, now, l.id, catOf(err1), catOf(err2))
				if catOf(err1) != catOf(err2) {
					fail("CANCEL leave=%d now=%d: main=%s naive=%s", l.id, now, catOf(err1), catOf(err2))
				}

			case op < 90: // 提前结束
				l, ok := pickLeave(emp)
				span := l.to - l.from + 2
				if span < 1 {
					span = 1
				}
				newEnd := l.from + rng.Intn(span) - 1
				if !ok {
					newEnd = rng.Intn(2000)
				}
				err1 := svc.EarlyEnd(now, emp, l.id, newEnd)
				err2 := nsv.EarlyEnd(now, emp, l.id, newEnd)
				log("step=%d EARLYEND emp=%s now=%d leave=%d newEnd=%d -> main=%s naive=%s",
					step, emp, now, l.id, newEnd, catOf(err1), catOf(err2))
				if catOf(err1) != catOf(err2) {
					fail("EARLYEND leave=%d newEnd=%d now=%d: main=%s naive=%s", l.id, newEnd, now, catOf(err1), catOf(err2))
				}

			default: // 余额查询（含历史时刻）
				qnow := now - rng.Intn(1500)
				b1, err1 := svc.Balance(qnow, emp)
				b2, err2 := nsv.Balance(qnow, emp)
				log("step=%d BALANCE emp=%s now=%d -> main(%+v %s) naive(%+v %s)",
					step, emp, qnow, b1, catOf(err1), b2, catOf(err2))
				if catOf(err1) != catOf(err2) {
					fail("BALANCE now=%d: main=%s naive=%s", qnow, catOf(err1), catOf(err2))
				}
				if err1 == nil && b1 != b2 {
					fail("BALANCE now=%d: main=%+v naive=%+v", qnow, b1, b2)
				}
			}

			// 周期性全员工当前时刻余额对照
			if step%25 == 24 {
				for _, e := range emps {
					b1, err1 := svc.Balance(now, e)
					b2, err2 := nsv.Balance(now, e)
					if catOf(err1) != catOf(err2) || (err1 == nil && b1 != b2) {
						fail("periodic BALANCE emp=%s now=%d: main(%+v %s) naive(%+v %s)",
							e, now, b1, catOf(err1), b2, catOf(err2))
					}
				}
			}
		}
		t.Logf("trial=%d seed=%d done: %d steps, final now=%d, cfg=%+v", trial, seed, steps, now, cfg)
	}
}
