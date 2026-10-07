// compensation-demo 演示动作副作用补偿回滚子系统：
// 打印每次动作的输入、每一步的生效或撤销结果，以及最终判定依据。
package main

import (
	"context"
	"fmt"

	"ontology/compensation"
)

func main() {
	graph := compensation.NewGraph()
	graph.AddObject("alice", map[string]any{"status": "active", "score": 10})
	graph.AddObject("bob", map[string]any{"status": "active", "score": 20})
	graph.AddObject("carol", map[string]any{"status": "active", "score": 30})

	exec := compensation.New(graph, compensation.NewLogTracer())
	ctx := context.Background()

	fmt.Println("========== 场景 1：步骤 3 业务拒绝，前两步逆序撤销 ==========")
	exec.Execute(ctx, &compensation.Action{ID: "transfer-fail", Ops: []compensation.SubOp{
		{Kind: compensation.OpSetProperties, ObjectID: "alice",
			Sets: map[string]any{"score": 0}},
		{Kind: compensation.OpCreateLink, LinkID: "edge1",
			From: "alice", To: "bob", LinkType: "owes"},
		{Kind: compensation.OpHook, ObjectID: "carol", Hook: "validate-budget"},
		{Check: func() bool { return false }}, // 业务校验拒绝
	}})

	fmt.Println("========== 场景 2：补偿中步骤 1 的逆操作返回失败、步骤 0 抛异常 ==========")
	exec.Execute(ctx, &compensation.Action{ID: "cascade-bad", Ops: []compensation.SubOp{
		{Kind: compensation.OpSetProperties, ObjectID: "alice",
			Sets: map[string]any{"score": 100}, InjectInversePanic: true},
		{Kind: compensation.OpSetProperties, ObjectID: "bob",
			Sets: map[string]any{"score": 200}, InjectInverseFailure: true},
		{Kind: compensation.OpSetProperties, ObjectID: "carol",
			Sets: map[string]any{"score": 300}},
		{Check: func() bool { return false }},
	}})

	if info, tainted := graph.Tainted("alice"); tainted {
		fmt.Printf(">>> alice 已污染：最早失败子操作编号=%d 登记编号=#%d\n",
			info.EarliestStep, info.Entry)
	}

	fmt.Println("========== 场景 3：污染态实例参与新动作，执行前被拒绝 ==========")
	out := exec.Execute(ctx, &compensation.Action{ID: "after-taint", Ops: []compensation.SubOp{
		{Kind: compensation.OpSetProperties, ObjectID: "carol",
			Sets: map[string]any{"score": 999}},
		{Kind: compensation.OpSetProperties, ObjectID: "alice",
			Sets: map[string]any{"score": 1}},
	}})
	fmt.Printf(">>> 拒绝前生效步骤数=%d，最终类别=%s\n", len(out.Applied), out.Category)

	fmt.Println("========== 场景 4：再次卷入 alice，最早编号不被覆盖 ==========")
	if info, tainted := graph.Tainted("alice"); tainted {
		fmt.Printf(">>> alice 污染最早编号仍为=%d（不被最近污染覆盖）\n", info.EarliestStep)
	}
}
