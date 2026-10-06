package narledger

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// opKind 是差分测试生成的操作种类。
type opKind int

const (
	opGrant opKind = iota
	opRevoke
	opReceive
	opDispense
	opSettle
	opResolve
	opDestroy
	opQueryLocked
	opQueryBalance
)

type genOp struct {
	kind                           opKind
	now                            int64
	person                         string
	start, end                     int64
	at                             int64
	drug, lot, id, dept, applicant string
	qty, expireAt                  int64
	r1, r2                         string
	used, returned, residue        int64
}

func applyReal(l *Ledger, o genOp) error {
	switch o.kind {
	case opGrant:
		return l.RegisterGrant(o.person, o.start, o.end)
	case opRevoke:
		return l.RevokeGrant(o.person, o.at)
	case opReceive:
		return l.Receive(o.now, o.drug, o.lot, o.qty, o.expireAt)
	case opDispense:
		return l.Dispense(o.now, o.id, o.dept, o.applicant, o.drug, o.qty, o.r1, o.r2)
	case opSettle:
		return l.Settle(o.now, o.id, o.used, o.returned, o.residue)
	case opResolve:
		return l.ResolveDiscrepancy(o.now, o.id, o.r1, o.r2)
	case opDestroy:
		return l.Destroy(o.now, o.drug, o.lot, o.qty, o.r1, o.r2)
	case opQueryLocked:
		_, err := l.DeptLocked(o.now, o.dept)
		return err
	case opQueryBalance:
		_, err := l.BatchBalance(o.now, o.drug, o.lot)
		return err
	}
	return nil
}

func applyNaive(n *naiveLedger, o genOp) error {
	switch o.kind {
	case opGrant:
		return n.grant(o.person, o.start, o.end)
	case opRevoke:
		return n.revoke(o.person, o.at)
	case opReceive:
		return n.receive(o.now, o.drug, o.lot, o.qty, o.expireAt)
	case opDispense:
		return n.dispense(o.now, o.id, o.dept, o.applicant, o.drug, o.qty, o.r1, o.r2)
	case opSettle:
		return n.settle(o.now, o.id, o.used, o.returned, o.residue)
	case opResolve:
		return n.resolve(o.now, o.id, o.r1, o.r2)
	case opDestroy:
		return n.destroy(o.now, o.drug, o.lot, o.qty, o.r1, o.r2)
	case opQueryLocked, opQueryBalance:
		// 朴素模型的只读查询仅做时钟回退判定
		if o.now < n.lastNow {
			return &OpError{Code: ErrClockRollback}
		}
		if o.kind == opQueryBalance {
			found := false
			for _, b := range n.batches {
				if b.drug == o.drug && b.lot == o.lot {
					found = true
					break
				}
			}
			if !found {
				return &OpError{Code: ErrNotFound}
			}
		}
	}
	return nil
}

func opDesc(o genOp) string {
	switch o.kind {
	case opGrant:
		return fmt.Sprintf("RegisterGrant(person=%s,[%d,%d))", o.person, o.start, o.end)
	case opRevoke:
		return fmt.Sprintf("RevokeGrant(person=%s,at=%d)", o.person, o.at)
	case opReceive:
		return fmt.Sprintf("Receive(now=%d,drug=%s,lot=%s,qty=%d,expire=%d)", o.now, o.drug, o.lot, o.qty, o.expireAt)
	case opDispense:
		return fmt.Sprintf("Dispense(now=%d,id=%s,dept=%s,app=%s,drug=%s,qty=%d,r=%s,%s)",
			o.now, o.id, o.dept, o.applicant, o.drug, o.qty, o.r1, o.r2)
	case opSettle:
		return fmt.Sprintf("Settle(now=%d,id=%s,used=%d,ret=%d,res=%d)", o.now, o.id, o.used, o.returned, o.residue)
	case opResolve:
		return fmt.Sprintf("ResolveDiscrepancy(now=%d,id=%s,r=%s,%s)", o.now, o.id, o.r1, o.r2)
	case opDestroy:
		return fmt.Sprintf("Destroy(now=%d,drug=%s,lot=%s,qty=%d,r=%s,%s)", o.now, o.drug, o.lot, o.qty, o.r1, o.r2)
	case opQueryLocked:
		return fmt.Sprintf("DeptLocked(now=%d,dept=%s)", o.now, o.dept)
	default:
		return fmt.Sprintf("BatchBalance(now=%d,drug=%s,lot=%s)", o.now, o.drug, o.lot)
	}
}

const diffSequences = 1500

// TestDifferentialVsNaive 与独立朴素模型对照 >=1500 组随机操作序列，
// 每步打印输入、输出与判定依据；同时校验重放确定性与账面不变量。
func TestDifferentialVsNaive(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "narledger-diff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "diff.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	totalSteps := 0
	for seq := 0; seq < diffSequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq) + 1))
		real1 := New().WithLogger(logFile)
		real2 := New() // 重放实例（无日志）
		naive := newNaive()
		ops := generateOps(rng)
		fmt.Fprintf(logFile, "==== SEQUENCE %d ops=%d ====\n", seq, len(ops))
		for step, o := range ops {
			totalSteps++
			e1 := applyReal(real1, o)
			e2 := applyReal(real2, o)
			en := applyNaive(naive, o)
			c1, cn := codeOf(e1), codeOf(en)
			label := func(c ErrorCode) string {
				if c == 0 {
					return "ACCEPT"
				}
				return codeName[c]
			}
			result := "ACCEPT"
			if c1 != 0 {
				result = fmt.Sprintf("REJECT code=%s reason=%q", c1, e1.Error())
			}
			fmt.Fprintf(logFile, "[seq=%d step=%d] INPUT %s => OUTPUT %s | 判定依据: 实际=%s 朴素=%s\n",
				seq, step, opDesc(o), result, label(c1), label(cn))
			if c1 != cn {
				t.Fatalf("seq=%d step=%d\n输入=%s\n实际 code=%s (%v)\n朴素 code=%s (%v)",
					seq, step, opDesc(o), c1, e1, cn, en)
			}
			if c2 := codeOf(e2); c2 != c1 {
				t.Fatalf("重放结果不一致 seq=%d step=%d: %s vs %s", seq, step, c1, c2)
			}
		}
		s1, s2, sn := real1.Snapshot(), real2.Snapshot(), naive.snapshot()
		if d := EqualSnap(s1, sn); d != "" {
			t.Fatalf("seq=%d 实际 vs 朴素快照差异: %s", seq, d)
		}
		if d := EqualSnap(s1, s2); d != "" {
			t.Fatalf("seq=%d 重放快照差异: %s", seq, d)
		}
		if err := real1.CheckInvariant(); err != nil {
			t.Fatalf("seq=%d 不变量: %v", seq, err)
		}
		// 锁定判定也与朴素全量扫描对照（在最后操作时刻附近取点）
		for _, d := range []string{"D0", "D1", "D2"} {
			probe := real1.clk.lastNow
			got, _ := real1.DeptLocked(probe, d)
			want := naive.deptLocked(d, probe)
			if got != want {
				t.Fatalf("seq=%d 科室 %s 锁定判定 实际=%v 朴素=%v", seq, d, got, want)
			}
		}
	}
	t.Logf("差分对照完成: %d 组序列, 共 %d 步；逐步日志: %s", diffSequences, totalSteps, logPath)
}

// generateOps 生成一条单调时间、实体受限的随机操作序列。
func generateOps(rng *rand.Rand) []genOp {
	const people = 6
	const drugs = 3
	const depts = 3
	var ops []genOp
	var now int64
	personName := func(i int) string { return fmt.Sprintf("P%d", i) }

	// 预置若干授权，保证部分操作能成功
	for i := 0; i < people; i++ {
		st := rng.Int63n(5)
		en := st + 1 + rng.Int63n(200000)
		ops = append(ops, genOp{kind: opGrant, person: personName(i), start: st, end: en})
	}

	n := 120 + rng.Intn(120)
	// 已存在的药品批次 / 单据，供后续操作引用
	type lotInfo struct {
		drug, lot string
		expire    int64
	}
	var lots []lotInfo
	var openOrders, dispOrders []string

	for i := 0; i < n; i++ {
		now += rng.Int63n(3) // 单调不降
		switch rng.Intn(12) {
		case 0:
			p := personName(rng.Intn(people))
			ops = append(ops, genOp{kind: opRevoke, person: p, at: now})
		case 1:
			p := personName(rng.Intn(people))
			st := now
			ops = append(ops, genOp{kind: opGrant, person: p, start: st, end: st + 1 + rng.Int63n(100000)})
		case 2, 3:
			drug := fmt.Sprintf("M%d", rng.Intn(drugs))
			lot := fmt.Sprintf("L%d", len(lots))
			expire := now
			switch rng.Intn(3) {
			case 0:
				expire = now + 1 + rng.Int63n(500) // 近期（可能在序列内过期）
			case 1:
				expire = now + 100000
			case 2:
				expire = now // 入库即过期
			}
			lots = append(lots, lotInfo{drug, lot, expire})
			ops = append(ops, genOp{kind: opReceive, now: now, drug: drug, lot: lot,
				qty: 1 + rng.Int63n(8), expireAt: expire})
		case 4, 5, 6:
			id := fmt.Sprintf("O%d", len(dispOrders))
			drug := fmt.Sprintf("M%d", rng.Intn(drugs))
			dept := fmt.Sprintf("D%d", rng.Intn(depts))
			r1 := personName(rng.Intn(people))
			r2 := personName(rng.Intn(people))
			app := personName(rng.Intn(people))
			ops = append(ops, genOp{kind: opDispense, now: now, id: id, dept: dept, applicant: app,
				drug: drug, qty: 1 + rng.Int63n(10), r1: r1, r2: r2})
			openOrders = append(openOrders, id)
			dispOrders = append(dispOrders, id)
		case 7, 8:
			id := ""
			if len(openOrders) > 0 {
				k := rng.Intn(len(openOrders))
				id = openOrders[k]
				openOrders = append(openOrders[:k], openOrders[k+1:]...)
			} else {
				id = fmt.Sprintf("O%d", rng.Intn(40))
			}
			q := rng.Int63n(12)
			u := rng.Int63n(q + 1)
			r := rng.Int63n(q - u + 1)
			res := q - u - r
			// 偶尔制造超额
			if rng.Intn(5) == 0 {
				u += 5
			}
			ops = append(ops, genOp{kind: opSettle, now: now, id: id, used: u, returned: r, residue: res})
		case 9:
			id := ""
			if len(dispOrders) > 0 {
				id = dispOrders[rng.Intn(len(dispOrders))]
			}
			ops = append(ops, genOp{kind: opResolve, now: now, id: id,
				r1: personName(rng.Intn(people)), r2: personName(rng.Intn(people))})
		case 10:
			var drug, lot string
			if len(lots) > 0 && rng.Intn(4) != 0 {
				info := lots[rng.Intn(len(lots))]
				drug, lot = info.drug, info.lot
			} else {
				drug, lot = "M0", "ghost"
			}
			ops = append(ops, genOp{kind: opDestroy, now: now, drug: drug, lot: lot,
				qty: 1 + rng.Int63n(10), r1: personName(rng.Intn(people)), r2: personName(rng.Intn(people))})
		default:
			if rng.Intn(2) == 0 {
				ops = append(ops, genOp{kind: opQueryLocked, now: now, dept: fmt.Sprintf("D%d", rng.Intn(depts))})
			} else if len(lots) > 0 {
				info := lots[rng.Intn(len(lots))]
				ops = append(ops, genOp{kind: opQueryBalance, now: now, drug: info.drug, lot: info.lot})
			}
		}
	}
	return ops
}
