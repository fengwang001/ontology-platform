package cpe

import (
	"fmt"
	"math/rand"
	"testing"
)

func digest(v View) string {
	if len(v.Cycles) == 0 {
		return fmt.Sprintf("cert=%s cycles=0", v.Cert)
	}
	c := v.Cycles[len(v.Cycles)-1]
	return fmt.Sprintf("cert=%s cyc#%d/%s raw=%+v counted=%+v pass=%v",
		v.Cert, c.Index, c.Phase, c.Raw, c.Counted, c.Pass)
}

// 与独立朴素模型对照大量随机操作序列：每步打印输入、输出与判定依据，
// 并比对主实现与朴素模型的错误类别、记录 ID 与全部持证人的完整视图。
func TestDifferentialRandomOps(t *testing.T) {
	seeds := []int64{7, 19, 42, 1337, 2024, 65535, 8888, 314159}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomSeq(t, seed, 700)
		})
	}
}

func runRandomSeq(t *testing.T, seed int64, steps int) {
	rng := rand.New(rand.NewSource(seed))
	cfg := Config{
		CycleLengthDays:      2 + rng.Intn(6),
		TotalRequired:        rng.Intn(16),
		MandatoryMin:         rng.Intn(6),
		MandatoryCap:         rng.Intn(10),
		ElectiveCap:          rng.Intn(10),
		GraceDays:            rng.Intn(4),
		CarryoverCap:         rng.Intn(6),
		CorrectionWindowDays: rng.Intn(6),
	}
	t.Logf("config=%+v", cfg)
	s, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	n := newNaive(cfg)

	holders := []string{"h0", "h1", "h2"}
	orgs := []string{"o0", "o1", "o2", "o3"}
	known := map[string][]string{} // holder -> 已接受记录 ID
	recOrg := map[string]string{}  // 记录 ID -> 出具机构
	now := 0

	fail := func(step int, format string, args ...interface{}) {
		t.Helper()
		msg := fmt.Sprintf(format, args...)
		for _, hid := range holders {
			mv, merr := s.GetAccounting(hid)
			nv, nerr := n.nView(hid, now)
			t.Logf("holder %s: main(err=%v):\n%s", hid, merr, mv)
			t.Logf("holder %s: naive(err=%v):\n%s", hid, nerr, nv)
		}
		t.Fatalf("step %d: %s", step, msg)
	}

	compareViews := func(step int) {
		t.Helper()
		for _, hid := range holders {
			mv, merr := s.GetAccounting(hid)
			nv, nerr := n.nView(hid, now)
			if ErrKindOf(merr) != nerr {
				fail(step, "GetAccounting(%s) err: main=%v naive=%s", hid, merr, nerr)
			}
			if merr == nil && !viewsEqual(mv, nv) {
				fail(step, "GetAccounting(%s) view mismatch", hid)
			}
		}
	}

	for i := 0; i < steps; i++ {
		if rng.Intn(100) < 55 {
			now += rng.Intn(3)
		}
		if rng.Intn(100) < 5 {
			now += rng.Intn(25) // 偶发大跨度跳跃，穿越多个周期
		}
		opNow := now
		rollback := rng.Intn(100) < 4
		if rollback {
			opNow = now - 1 - rng.Intn(2)
		}
		hid := holders[rng.Intn(len(holders))]
		kind := rng.Intn(100)
		var mainErr ErrKind
		var naiveErr ErrKind
		var desc string

		switch {
		case kind < 12: // 注册/重新注册持证人
			if rng.Intn(100) < 5 {
				hid = "ghost"
			}
			issue := now - rng.Intn(4)
			if rng.Intn(100) < 3 {
				issue = -1
			}
			mainErr = ErrKindOf(s.RegisterHolder(hid, issue, opNow))
			naiveErr = n.registerHolder(hid, issue, opNow)
			desc = fmt.Sprintf("RegisterHolder id=%s issue=%d now=%d", hid, issue, opNow)
			if mainErr == ErrNone {
				known[hid] = nil // 新注册/换发：旧记录清空
			}
		case kind < 55: // 登记学分
			if rng.Intn(100) < 4 {
				hid = "ghost"
			}
			cat := Category(rng.Intn(3))
			if rng.Intn(100) < 2 {
				cat = Category(7)
			}
			credits := 1 + rng.Intn(8)
			if rng.Intn(100) < 3 {
				credits = 0
			}
			earned := now - rng.Intn(20)
			if rng.Intn(100) < 3 {
				earned = now + 1
			}
			org := orgs[rng.Intn(len(orgs))]
			recID, err := s.RegisterCredit(hid, cat, credits, earned, org, opNow)
			mainErr = ErrKindOf(err)
			nRecID, nErr := n.registerCredit(hid, cat, credits, earned, org, opNow)
			naiveErr = nErr
			desc = fmt.Sprintf("RegisterCredit h=%s cat=%s credits=%d earned=%d org=%s now=%d",
				hid, cat, credits, earned, org, opNow)
			if recID != nRecID {
				fail(i, "%s => recordID main=%q naive=%q", desc, recID, nRecID)
			}
			if mainErr == ErrNone {
				known[hid] = append(known[hid], recID)
				recOrg[recID] = org
			}
		case kind < 75: // 更正
			recID := "R99999999"
			org := orgs[rng.Intn(len(orgs))]
			if recs := known[hid]; len(recs) > 0 && rng.Intn(100) < 80 {
				recID = recs[rng.Intn(len(recs))]
				if rng.Intn(100) < 70 {
					org = recOrg[recID]
				}
			}
			newCredits := 1 + rng.Intn(8)
			if rng.Intn(100) < 3 {
				newCredits = 0
			}
			mainErr = ErrKindOf(s.CorrectCredit(hid, recID, org, newCredits, opNow))
			naiveErr = n.correct(hid, recID, org, newCredits, opNow)
			desc = fmt.Sprintf("CorrectCredit h=%s rec=%s org=%s new=%d now=%d", hid, recID, org, newCredits, opNow)
		case kind < 90: // 撤销
			recID := "R99999999"
			org := orgs[rng.Intn(len(orgs))]
			if recs := known[hid]; len(recs) > 0 && rng.Intn(100) < 80 {
				recID = recs[rng.Intn(len(recs))]
				if rng.Intn(100) < 70 {
					org = recOrg[recID]
				}
			}
			mainErr = ErrKindOf(s.RevokeCredit(hid, recID, org, opNow))
			naiveErr = n.revoke(hid, recID, org, opNow)
			desc = fmt.Sprintf("RevokeCredit h=%s rec=%s org=%s now=%d", hid, recID, org, opNow)
		default: // 历史时刻查询
			asOf := 0
			if now > 0 {
				asOf = rng.Intn(now + 1)
			}
			mv, merr := s.GetAccountingAt(hid, asOf)
			nv, nerr2 := n.nView(hid, asOf)
			mainErr, naiveErr = ErrKindOf(merr), nerr2
			desc = fmt.Sprintf("GetAccountingAt h=%s asOf=%d", hid, asOf)
			if mainErr == ErrNone && naiveErr == ErrNone && !viewsEqual(mv, nv) {
				fail(i, "%s => historical view mismatch\nmain:\n%s\nnaive:\n%s", desc, mv, nv)
			}
		}

		if mainErr != naiveErr {
			fail(i, "%s => err main=%s naive=%s", desc, mainErr, naiveErr)
		}
		// 判定依据：记录本步输入、输出与目标持证人当前周期摘要。
		if mv, err := s.GetAccounting(hid); err == nil {
			t.Logf("step %d: %s => %s | %s", i, desc, mainErr, digest(mv))
		} else {
			t.Logf("step %d: %s => %s | holder view err=%v", i, desc, mainErr, err)
		}
		// 时钟一致性：被接受的操作推进时钟，被拒绝的不推进。
		mainNow, mainHas := s.LastNow()
		if mainHas != n.hasLast || (mainHas && mainNow != n.lastNow) {
			fail(i, "clock mismatch: main=(%d,%v) naive=(%d,%v)", mainNow, mainHas, n.lastNow, n.hasLast)
		}
		if mainHas {
			now = mainNow
		}
		compareViews(i)
	}
}
