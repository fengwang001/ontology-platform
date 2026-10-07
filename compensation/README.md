# compensation — 动作副作用补偿回滚

动作的多个副作用子操作（属性修改、链接创建/删除、级联校验钩子）按序生效；
任一子操作失败时，已生效副作用严格按逆序补偿。详见 [`DESIGN.md`](DESIGN.md)。

## 快速使用

```go
graph := compensation.NewGraph()
graph.AddObject("alice", map[string]any{"score": 10})
exec := compensation.New(graph, compensation.NewLogTracer())

out := exec.Execute(ctx, &compensation.Action{
    ID: "a1",
    Ops: []compensation.SubOp{
        {Kind: compensation.OpSetProperties, ObjectID: "alice",
         Sets: map[string]any{"score": 0}},
        {Check: func() bool { return false }}, // 业务拒绝 → 触发补偿
    },
})
fmt.Println(out.Category) // BUSINESS_REJECTED；alice.score 已恢复为 10
```

## 结果类别（固定优先级，从高到低）

- `CONTAMINATED`：涉及对象已污染，动作在任何子操作前被拒绝。
- `BUSINESS_REJECTED`：子操作业务/结构校验拒绝，此前副作用已干净补偿。
- `CONTENTION`：与并发动作争用同一属性/链接，拒绝发生在任何改动之前。
- `COMPENSATION_FAILED`：补偿中有逆操作失败或异常，失败对象被冻结并标记污染。
- `OK`：全部子操作生效。

## 故障注入

`SubOp.InjectInverseFailure` / `InjectInversePanic` 用于测试补偿失败与异常路径；
`SubOp.Check` 返回 `false` 用于在指定步骤注入业务拒绝。

## 测试

```bash
go test -race -v ./compensation/
go test -race -count=10 ./...
go run ./cmd/compensation-demo
```
