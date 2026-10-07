// server 是变更流消费与补偿子系统的演示入口：构造一套持久化状态，
// 注册补偿计划，演示正常补偿、重复投递去重、崩溃续作与撤销规则。
package main

import (
	"fmt"
	"time"

	"ontology/ontology"
)

func main() {
	st := ontology.NewStorage(time.Now)
	st.Store.Put(ontology.Object{ID: "account-a", Fields: map[string]int{"balance": 100}})
	st.Store.Put(ontology.Object{ID: "account-b", Fields: map[string]int{"balance": 50}})

	builders := map[string]ontology.CompensationBuilder{
		// 转账动作的补偿：从收款方扣回、向付款方返还（两项副作用，原子生效）。
		"transfer": func(evt ontology.ChangeEvent) ([]ontology.SideEffect, error) {
			amount, _ := evt.Payload["amount"].(int)
			return []ontology.SideEffect{
				{ObjectID: "account-b", Op: ontology.OpAdd, Field: "balance", Value: -amount},
				{ObjectID: "account-a", Op: ontology.OpAdd, Field: "balance", Value: amount},
			}, nil
		},
	}
	consumer := ontology.NewConsumer(st, builders)

	evt := ontology.ChangeEvent{
		EventID: "evt-1", ActionExecutionID: "act-1",
		Kind: ontology.KindActionSucceeded, ActionType: "transfer",
		Payload: map[string]any{"amount": 30},
	}

	fmt.Println("== 首次消费 ==")
	report(consumer.Consume(evt), st)

	fmt.Println("== 重复投递（网络重试） ==")
	report(consumer.Consume(evt), st)

	fmt.Println("== 独立等价调用（新标识，相同参数） ==")
	evt2 := evt
	evt2.EventID = "evt-2"
	evt2.ActionExecutionID = "act-2"
	report(consumer.Consume(evt2), st)

	fmt.Println("== 决策日志（供事后核查） ==")
	for _, rec := range st.Dedup.Decisions() {
		fmt.Printf("  #%d event=%s action=%s verdict=%s compared=%q reason=%s\n",
			rec.Seq, rec.EventID, rec.ActionExecutionID, rec.Verdict,
			rec.ComparedAgainst, rec.Reason)
	}
}

func report(res ontology.ConsumeResult, st *ontology.Storage) {
	fmt.Printf("  结果: %v", res.Outcome)
	if res.Err != nil {
		fmt.Printf(" (%v)", res.Err)
	}
	fmt.Println()
	for id, obj := range st.Store.Snapshot() {
		fmt.Printf("  %s: %v\n", id, obj.Fields)
	}
}
