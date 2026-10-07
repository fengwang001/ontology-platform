// auditdemo 是动作事务审计溯源子系统的端到端演示：
// 注册实例 → 提交/回退/审计失败 → 订正 → 区间重放 → 篡改检测。
package main

import (
	"errors"
	"fmt"

	"ontology/audit"
)

func main() {
	store := audit.NewStateStore()
	log := audit.NewAuditLog()
	exec := audit.NewExecutor(store, log)
	replayer := audit.NewReplayer(log, 2)
	naive := audit.NewNaiveModel(log)

	const typ = "Order"

	reg, _ := exec.RegisterInstance(typ, "o1", "init")
	fmt.Println("[注册]", reg)

	ok, _ := exec.Execute(audit.Action{
		ActionID: "place-order", TypeName: typ,
		Writes: []audit.Write{{Instance: "o1", Value: "placed"}},
	}, nil)
	fmt.Println("[提交]", ok)

	rb, _ := exec.Execute(audit.Action{
		ActionID: "cancel-attempt", TypeName: typ,
		Writes: []audit.Write{{Instance: "o1", Value: "cancelled"}},
	}, func(*audit.Txn) error { return audit.ErrActionRolledBack })
	fmt.Println("[回退]", rb, "（占序号，Before==After，状态不变）")

	log.InjectNextAppendFailure(errors.New("simulated disk failure"))
	_, err := exec.Execute(audit.Action{
		ActionID: "ship-attempt", TypeName: typ,
		Writes: []audit.Write{{Instance: "o1", Value: "shipped"}},
	}, nil)
	fmt.Printf("[审计失败] %v  → 状态=%q，未占序号\n", err, live(store, typ, "o1"))

	ship, _ := exec.Execute(audit.Action{
		ActionID: "ship", TypeName: typ,
		Writes: []audit.Write{{Instance: "o1", Value: "shipped"}},
	}, nil)
	fmt.Println("[提交]", ship)

	corr, err := exec.Correct(typ, "fix-place-order", ok.Seq, map[string]string{"o1": "placed-v2"})
	if err != nil {
		panic(err)
	}
	fmt.Println("[订正]", corr, "（新序号；原记录保留）")

	for seq := int64(0); seq <= log.LastSeq(typ); seq++ {
		st, _ := replayer.StateAt(typ, seq)
		fmt.Printf("重放@%d => %v\n", seq, st)
	}

	rangeRes, _ := replayer.Replay(typ, ship.Seq, corr.Seq)
	fmt.Printf("区间 (%d,%d] 差异: %+v\n", ship.Seq, corr.Seq, rangeRes.Events)

	nres, _ := naive.Replay(typ, 0, log.LastSeq(typ))
	fmt.Printf("朴素线性重放终态: %v\n", nres.StateTo)

	if err := log.VerifyChain(typ); err != nil {
		panic(err)
	}
	fmt.Println("哈希链校验: OK")
}

func live(s *audit.StateStore, typ, inst string) string {
	v, _ := s.Get(typ, inst)
	return v
}
