# compensation — 动作副作用补偿回滚子系统

并行副作用分支 + 依赖感知的 Saga 式补偿引擎。详细设计与取舍见
[`DESIGN.md`](./DESIGN.md)。

## 能力

- 动作声明多条副作用分支，分支间依赖必须构成 DAG（环在声明阶段、
  任何生效之前被拒绝，对象图零改动）。
- 无依赖分支并发生效；分支内子操作严格顺序；子操作失败与上游
  失败都沿依赖链传播，下游未开始的子操作永不开始。
- 补偿按依赖逆向拓扑调度：被依赖者等全部下游补偿完成后才撤销；
  分支内逆序。无依赖分支**允许并发补偿**（终态与任一合法拓扑
  顺序等价，见 `DESIGN.md` 2.3）。
- 补偿就绪判定为 O(1)（每分支一个原子剩余下游计数，不遍历分支
  集合）。
- 外部直接补偿请求违反次序时被拒绝，状态与计数不变，拒绝有日志。
- 多个逆操作失败分别记录（分支 + 子操作下标），互不遮蔽。
- 跨动作用键级严格两阶段锁保证可串行化；锁按字典序获取，无死锁。
- 固定错误优先级：依赖环 > 上游被动失败 > 子操作自身失败 >
  补偿顺序违例 > 逆操作失败。

## 最小用法

```go
g := compensation.NewGraph()
exec := compensation.NewExecutor(g, compensation.NewLockManager(), logger)

action, err := exec.Declare("ship-order", []compensation.BranchSpec{
    {Name: "reserve", Steps: []compensation.StepSpec{
        reserveSeatStep,       // 实现 compensation.Operation
    }},
    {Name: "charge", DependsOn: []string{"reserve"}, Steps: []compensation.StepSpec{
        chargeStep,            // MakeInverse 返回退款逆操作
    }},
})
if err != nil {
    // 声明被拒绝（如依赖环）：对象图没有任何改动
    return err
}

// 任一失败时 Execute 自动按逆向依赖完成补偿并返回分类报告；
// 全部成功返回 nil。
if report := action.Execute(); report != nil {
    switch report.Kind() {
    case compensation.KindUpstreamFailure: // ...
    case compensation.KindOperationFailure:
        for _, e := range report.ByKind(compensation.KindUndoFailure) {
            // e.BranchName / e.StepIndex 精确定位每个补偿失败
        }
    }
}
defer action.Release()

// 成功后也可按依赖次序手动补偿；上游在下游完成前会被拒绝：
action.DirectCompensate("charge")  // 先下游
action.DirectCompensate("reserve") // 再上游
```

## 日志

实现 `compensation.Logger` 即可收集每次补偿尝试：

```text
[comp-attempt] action=rand branch=E step=1 inverse=inv-E-s1
  input="keys=[key-4-1]" allowed=true result=undo-error
  reason=downstream-remaining=0; undo injected failure: inv-E-s1
```

## 测试

见 `DESIGN.md` 第 6 节。覆盖：无依赖并发补偿等价串行、依赖链
补偿顺序、环检测、直接补偿请求被拒绝、多分支补偿失败不遮蔽、
跨动作并发可串行化，以及 400 轮随机 DAG + 故障注入与朴素串行
模型的差分对拍。
