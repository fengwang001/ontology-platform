# 补偿回滚子系统使用文档

包：`ontology/compensate`

## 最小示例

```go
g := compensate.NewGraph()
g.AddObject(compensate.Object{ID: "o1", Attrs: compensate.Attrs{"v": 1}, Version: 1})

eng := compensate.NewEngine(g).WithTracer(&compensate.LogTracer{W: os.Stdout})
res := eng.Execute(compensate.Action{
    Name: "my-action",
    Ops: []compensate.SubOp{
        {Kind: compensate.OpSetAttrs, Object: "o1", Attrs: compensate.Attrs{"v": 2}},
        {Kind: compensate.OpCreateLink, Link: compensate.Link{ID: "k1", Source: "o1", Target: "o2"}},
        {Kind: compensate.OpValidate, Object: "o2"},
    },
})

if !res.Committed {
    // res.Primary: contaminated | business_reject | contention | compensation_failed
    // res.CompFailures: 每个补偿失败环节（op 编号 + 原因）
}

eng.Repair("o1") // 显式修复后解除污染态
```

## 判定优先级（固定，与补偿是否成功无关）

1. `ReasonContaminated` 实例污染
2. `ReasonBusinessReject` 业务校验拒绝（含前向 panic）
3. `ReasonContention` 并发争用
4. `ReasonCompensationFailed` 逆操作失败/异常

## 故障注入（测试用）

`Action.Inject` 以 op 编号为键：

- `Apply[i] = FaultApplyFail | FaultApplyPanic`
- `Undo[i]  = FaultUndoFail  | FaultUndoPanic`

## 轨迹

实现 `Tracer` 接口（或直接用 `LogTracer` / `SliceTracer`）即可收到
每个 apply/undo 步骤的生效、撤销、拒绝、失败、panic 与最终 verdict 事件。
