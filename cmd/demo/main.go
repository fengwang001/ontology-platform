// Command demo 演示动作前置/后置校验分离机制的四类结果路径：
// 接受、前置失败（返回全部失败条件）、后置失败（计划整体放弃）、
// 目标被并发撤销；以及声明自相矛盾在注册期即被拒绝。
package main

import (
	"context"
	"fmt"

	"ontology/actionguard"
)

func main() {
	st := actionguard.NewStore()
	ex := actionguard.NewExecutor(st)

	if err := ex.Register(actionguard.NewTransferAction()); err != nil {
		panic(err)
	}
	if err := ex.Register(actionguard.NewWithdrawFeeAction(10)); err != nil {
		panic(err)
	}
	// 自相矛盾的动作声明：注册期立即失败，永远不会被执行。
	if err := ex.Register(actionguard.NewContradictoryTransfer()); err != nil {
		fmt.Printf("register rejected: %v\n", err)
	}

	st.CreateObject("a", map[string]string{"balance": "100"})
	st.CreateObject("b", map[string]string{"balance": "20"})

	run := func(tag, typ, callID string, in map[string]any, targets []string) {
		out := ex.Execute(context.Background(), typ, callID, in, targets)
		fmt.Printf("[%s] class=%s accepted=%v failedPre=%v failedPost=%s\n",
			tag, out.Class, out.Accepted, out.FailedPre, out.FailedPost)
	}

	run("transfer ok", "transfer", "c1",
		map[string]any{"from": "a", "to": "b", "amount": "30", "total": "120"},
		[]string{"a", "b"})
	run("pre fail (all)", "transfer", "c2",
		map[string]any{"from": "a", "to": "ghost", "amount": "999", "total": "120"},
		[]string{"a", "ghost"})
	run("post fail (fee)", "withdraw_fee", "c3",
		map[string]any{"from": "a", "amount": "65"}, []string{"a"})

	st.Revoke("b")
	run("revoked target", "transfer", "c4",
		map[string]any{"from": "a", "to": "b", "amount": "1", "total": "120"},
		[]string{"a", "b"})

	a, verA, _, _ := st.ObjectState("a")
	b, verB, _, _ := st.ObjectState("b")
	fmt.Printf("final: a=%s(v%d) b=%s(v%d), accepted=%d, post-failures=%d\n",
		a["balance"], verA, b["balance"], verB, len(st.AcceptedCalls()), len(st.FailureTrail()))
}
