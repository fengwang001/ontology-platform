// 演示程序：装配本体平台核心（存储、注册表、执行引擎），
// 依次演示四类结果（接受、前置失败、后置失败、声明矛盾）
// 以及审计日志与失败轨迹的查询。
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	store := ontology.NewStore()
	registry := ontology.NewRegistry()
	// 注册一个转账动作：前置检查金额与余额，后置保证无负余额。
	transfer := ontology.ActionType{
		ID: "transfer",
		Preconditions: []ontology.PreCondition{
			{
				ID:         "positive-amount",
				Descriptor: ontology.AtomExpr("amount", ontology.CmpGt, 0),
				Eval: func(in ontology.PreInput) bool {
					return in.Params["amount"].(int64) > 0
				},
			},
			{
				ID: "sufficient-funds",
				Eval: func(in ontology.PreInput) bool {
					from, _ := in.State.GetObject(ontology.ObjectID(in.Params["from"].(string)))
					return from.Props["balance"].(int64) >= in.Params["amount"].(int64)
				},
			},
		},
		Postconditions: []ontology.PostCondition{
			{
				ID: "no-negative-balance",
				Eval: func(in ontology.PostInput) bool {
					from, _ := in.State.GetObject(ontology.ObjectID(in.Params["from"].(string)))
					to, _ := in.State.GetObject(ontology.ObjectID(in.Params["to"].(string)))
					return from.Props["balance"].(int64) >= 0 && to.Props["balance"].(int64) >= 0
				},
			},
		},
		Apply: func(ctx *ontology.ApplyContext) error {
			fromID := ontology.ObjectID(ctx.Params["from"].(string))
			toID := ontology.ObjectID(ctx.Params["to"].(string))
			amount := ctx.Params["amount"].(int64)
			from, _ := ctx.State.GetObject(fromID)
			to, _ := ctx.State.GetObject(toID)
			if err := ctx.Plan.Update(fromID, map[string]any{"balance": from.Props["balance"].(int64) - amount}); err != nil {
				return err
			}
			return ctx.Plan.Update(toID, map[string]any{"balance": to.Props["balance"].(int64) + amount})
		},
	}
	must(registry.Register(transfer))
	// 演示：自相矛盾的声明在定义阶段即被拒绝。
	contradictory := ontology.ActionType{
		ID: "bad-action",
		Preconditions: []ontology.PreCondition{
			{ID: "a", Excludes: []string{"b"}, Eval: func(ontology.PreInput) bool { return true }},
			{ID: "b", Eval: func(ontology.PreInput) bool { return true }},
		},
	}
	if err := registry.Register(contradictory); err != nil {
		fmt.Printf("[定义期] 矛盾声明被拒绝: %v\n\n", err)
	}
	store.Seed(ontology.Object{ID: "alice", Type: "account", Props: map[string]any{"balance": int64(100)}})
	store.Seed(ontology.Object{ID: "bob", Type: "account", Props: map[string]any{"balance": int64(50)}})
	exec := ontology.NewExecutor(store, registry)
	call := func(id, from, to string, amount int64) {
		res := exec.Execute(ontology.Call{
			ID: id, ActionType: "transfer",
			Params:  map[string]any{"from": from, "to": to, "amount": amount},
			Targets: []ontology.ObjectID{ontology.ObjectID(from), ontology.ObjectID(to)},
		})
		if res.Accepted() {
			fmt.Printf("[接受] %s commitSeq=%d\n", id, res.CommitSeq)
		} else {
			fmt.Printf("[拒绝] %s 类别=%s 详情=%s\n", id, res.Reject.Category, res.Reject.Detail)
		}
	}
	call("c1", "alice", "bob", 30)   // 接受
	call("c2", "alice", "bob", -10)  // 前置失败
	call("c3", "alice", "bob", 1000) // 前置失败（余额不足）
	call("c4", "alice", "ghost", 10) // 目标缺失（并发撤销类别）
	fmt.Println("\n-- 最终状态 --")
	for _, id := range store.ObjectIDs() {
		obj, _ := store.GetObject(id)
		fmt.Printf("%s: balance=%v version=%d\n", id, obj.Props["balance"], obj.Version)
	}
	fmt.Println("\n-- 校验审计日志 --")
	for _, rec := range store.ValidationLog() {
		fmt.Printf("#%d %s %s/%s 通过=%v 条件=%v\n",
			rec.Seq, rec.CallID, rec.ActionType, rec.Phase, rec.PassedAll, rec.Outcomes)
	}
	fmt.Println("\n-- 失败轨迹 --")
	for _, f := range store.FailureTrail() {
		fmt.Printf("#%d %s 类别=%s 详情=%s\n", f.Seq, f.CallID, f.Category, f.Detail)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
