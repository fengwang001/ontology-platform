package pivas_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/pivas"
)

// opCmd 一条可重放的操作指令。
type opCmd struct {
	kind    string // upsertDrug / addPair / removePair / registerBench / admit / cancel
	now     int64
	drug    pivas.Drug
	a, b    string
	bench   pivas.BenchConfig
	order   pivas.Order
	orderID string
}

func (c opCmd) String() string {
	switch c.kind {
	case "upsertDrug":
		return fmt.Sprintf("upsertDrug now=%d %+v", c.now, c.drug)
	case "addPair":
		return fmt.Sprintf("addPair now=%d %s %s", c.now, c.a, c.b)
	case "removePair":
		return fmt.Sprintf("removePair now=%d %s %s", c.now, c.a, c.b)
	case "registerBench":
		return fmt.Sprintf("registerBench now=%d %+v", c.now, c.bench)
	case "admit":
		return fmt.Sprintf("admit now=%d %+v", c.now, c.order)
	case "cancel":
		return fmt.Sprintf("cancel now=%d %s", c.now, c.orderID)
	}
	return "?"
}

// genOps 生成一条确定性的随机操作序列。
func genOps(r *rand.Rand, nSteps int) []opCmd {
	drugIDs := []string{"d0", "d1", "d2", "d3", "d4", "d5", "d6", "d7"}
	solvents := []string{"NS", "GS", "W"}
	benchIDs := []string{"b0", "b1", "b2"}
	var ops []opCmd
	var orderIDs []string
	now := int64(0)
	orderSeq := 0
	// 基础目录与洁净台，保证后续操作大多进入排程路径
	for i, id := range drugIDs {
		ops = append(ops, opCmd{kind: "upsertDrug", now: 0, drug: pivas.Drug{
			ID:             id,
			RoomStableSec:  20 + int64(r.Intn(200)),
			ColdStableSec:  20 + int64(r.Intn(200)),
			LightSensitive: i%4 == 0,
			SolventClass:   solvents[i%len(solvents)],
		}})
	}
	for _, id := range benchIDs {
		cap_ := 1 + r.Intn(3)
		durs := make([]int64, cap_+1)
		base := int64(5 + r.Intn(15))
		for j := 1; j <= cap_; j++ {
			durs[j] = base
			base += 1 + int64(r.Intn(15))
		}
		ops = append(ops, opCmd{kind: "registerBench", now: 0, bench: pivas.BenchConfig{
			ID: id, Capacity: cap_, DurationByCount: durs, ClearanceSec: 3 + int64(r.Intn(13)),
		}})
	}
	for step := 0; step < nSteps; step++ {
		// 时钟：多数前进，偶尔停留，偶尔尝试回退
		switch x := r.Intn(100); {
		case x < 5:
			now -= int64(r.Intn(10))
			if now < 0 {
				now = 0
			}
		case x < 15:
			// 停留
		default:
			now += int64(r.Intn(30))
		}
		switch x := r.Intn(100); {
		case x < 14:
			id := drugIDs[r.Intn(len(drugIDs))]
			ops = append(ops, opCmd{kind: "upsertDrug", now: now, drug: pivas.Drug{
				ID:             id,
				RoomStableSec:  5 + int64(r.Intn(300)),
				ColdStableSec:  5 + int64(r.Intn(300)),
				LightSensitive: r.Intn(5) == 0,
				SolventClass:   solvents[r.Intn(len(solvents))],
			}})
		case x < 22:
			a := drugIDs[r.Intn(len(drugIDs))]
			b := drugIDs[r.Intn(len(drugIDs))]
			ops = append(ops, opCmd{kind: "addPair", now: now, a: a, b: b})
		case x < 26:
			a := drugIDs[r.Intn(len(drugIDs))]
			b := drugIDs[r.Intn(len(drugIDs))]
			ops = append(ops, opCmd{kind: "removePair", now: now, a: a, b: b})
		case x < 34:
			cap_ := 1 + r.Intn(3)
			durs := make([]int64, cap_+1)
			base := int64(5 + r.Intn(15))
			for i := 1; i <= cap_; i++ {
				durs[i] = base
				base += 1 + int64(r.Intn(15))
			}
			ops = append(ops, opCmd{kind: "registerBench", now: now, bench: pivas.BenchConfig{
				ID: benchIDs[r.Intn(len(benchIDs))], Capacity: cap_,
				DurationByCount: durs, ClearanceSec: 3 + int64(r.Intn(13)),
			}})
		case x < 88:
			k := 1 + r.Intn(6)
			perm := r.Perm(len(drugIDs))
			drugs := make([]string, 0, k)
			for i := 0; i < k; i++ {
				if r.Intn(20) == 0 {
					drugs = append(drugs, "zz") // 偶发未登记药品
				} else {
					drugs = append(drugs, drugIDs[perm[i]])
				}
			}
			id := fmt.Sprintf("ord%d", orderSeq)
			orderSeq++
			orderIDs = append(orderIDs, id)
			ops = append(ops, opCmd{kind: "admit", now: now, order: pivas.Order{
				ID:            id,
				DrugIDs:       drugs,
				Solvent:       solvents[r.Intn(len(solvents))],
				RequiredAt:    now + int64(r.Intn(160)),
				Urgent:        r.Intn(4) == 0,
				LightProofBag: r.Intn(5) < 2,
			}})
		default:
			id := "ord-unknown"
			if len(orderIDs) > 0 && r.Intn(10) < 8 {
				id = orderIDs[r.Intn(len(orderIDs))]
			}
			ops = append(ops, opCmd{kind: "cancel", now: now, orderID: id})
		}
	}
	return ops
}

// runOptimized 在优化实现上重放操作序列，返回每步输出与最终快照。
func runOptimized(t *testing.T, ops []opCmd) ([]string, string) {
	t.Helper()
	c, err := pivas.NewCenter(diffRoomTransport, diffColdTransport)
	if err != nil {
		t.Fatal(err)
	}
	outs := make([]string, 0, len(ops))
	for _, op := range ops {
		var out string
		switch op.kind {
		case "upsertDrug":
			out = errString(c.UpsertDrug(op.now, op.drug))
		case "addPair":
			out = errString(c.AddIncompatibility(op.now, op.a, op.b))
		case "removePair":
			out = errString(c.RemoveIncompatibility(op.now, op.a, op.b))
		case "registerBench":
			out = errString(c.RegisterBench(op.now, op.bench))
		case "admit":
			a, err := c.Admit(op.now, op.order)
			if err != nil {
				out = errString(err)
			} else {
				out = fmt.Sprintf("OK bench=%s batch=%s storage=%s start=%d finish=%d delivery=%d",
					a.BenchID, a.BatchID, a.Storage, a.Start, a.Finish, a.Delivery)
			}
		case "cancel":
			out = errString(c.Cancel(op.now, op.orderID))
		}
		outs = append(outs, out)
		if err := c.VerifyInvariants(); err != nil {
			t.Fatalf("操作 %s 后不变量被破坏: %v", op, err)
		}
	}
	return outs, c.DumpState()
}

// runNaive 在朴素模型上重放操作序列，返回每步输出与最终快照。
func runNaive(ops []opCmd) ([]string, string) {
	n := newNaiveCenter(diffRoomTransport, diffColdTransport)
	outs := make([]string, 0, len(ops))
	for _, op := range ops {
		var out string
		switch op.kind {
		case "upsertDrug":
			out = errString(n.upsertDrug(op.now, op.drug))
		case "addPair":
			out = errString(n.addPair(op.now, op.a, op.b))
		case "removePair":
			out = errString(n.removePair(op.now, op.a, op.b))
		case "registerBench":
			out = errString(n.registerBench(op.now, op.bench))
		case "admit":
			a, err := n.admit(op.now, op.order)
			if err != nil {
				out = errString(err)
			} else {
				out = fmt.Sprintf("OK bench=%s batch=%s storage=%s start=%d finish=%d delivery=%d",
					a.BenchID, a.BatchID, a.Storage, a.Start, a.Finish, a.Delivery)
			}
		case "cancel":
			out = errString(n.cancel(op.now, op.orderID))
		}
		outs = append(outs, out)
	}
	return outs, n.dump()
}

const (
	diffRoomTransport = 7
	diffColdTransport = 13
)

func errString(err error) string {
	if err == nil {
		return "OK"
	}
	var pe *pivas.Error
	if ok := errorAs(err, &pe); ok {
		return fmt.Sprintf("ERR %d", pe.Code)
	}
	var ne *naiveError
	if errorAs(err, &ne) {
		return fmt.Sprintf("ERR %d", ne.code)
	}
	return "ERR ?"
}

func errorAs(err error, target any) bool {
	switch t := target.(type) {
	case **pivas.Error:
		if e, ok := err.(*pivas.Error); ok {
			*t = e
			return true
		}
	case **naiveError:
		if e, ok := err.(*naiveError); ok {
			*t = e
			return true
		}
	}
	return false
}

// 与独立朴素模型对照：不少于 1500 组随机操作序列，
// 逐步比对输入、输出与最终状态，并打印每步判定依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		ops := genOps(rand.New(rand.NewSource(int64(seq)*7919+13)), 40)
		optOuts, optDump := runOptimized(t, ops)
		naiveOuts, naiveDump := runNaive(ops)
		for i := range ops {
			if optOuts[i] != naiveOuts[i] {
				t.Fatalf("序列 %d 第 %d 步不一致\n输入: %s\n优化实现: %s\n朴素模型: %s",
					seq, i, ops[i], optOuts[i], naiveOuts[i])
			}
			t.Logf("seq=%d step=%d 输入=%s 输出=%s", seq, i, ops[i], optOuts[i])
		}
		if optDump != naiveDump {
			t.Fatalf("序列 %d 最终状态不一致\n优化实现:\n%s\n朴素模型:\n%s", seq, optDump, naiveDump)
		}
	}
}

// 相同操作序列重放得到完全相同的批次与时刻。
func TestReplayDeterminism(t *testing.T) {
	for seq := 0; seq < 20; seq++ {
		ops := genOps(rand.New(rand.NewSource(int64(seq)*97+5)), 60)
		outs1, dump1 := runOptimized(t, ops)
		outs2, dump2 := runOptimized(t, ops)
		if dump1 != dump2 {
			t.Fatalf("序列 %d 重放结果不同", seq)
		}
		for i := range outs1 {
			if outs1[i] != outs2[i] {
				t.Fatalf("序列 %d 第 %d 步重放输出不同: %s vs %s", seq, i, outs1[i], outs2[i])
			}
		}
	}
}
