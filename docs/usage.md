# 使用指南

## 构造与注册

```go
store := lifecycle.NewStore()
err := store.RegisterType(&lifecycle.ObjectType{
    Name:    "Order",
    States:  []lifecycle.State{"draft", "paid", "closed"},
    Initial: "draft",
    TerminalsList: []lifecycle.State{"closed"},
    Transitions: map[string]*lifecycle.TransitionRule{
        "pay": {
            Name: "pay",
            From: []lifecycle.State{"draft"},
            To:   "paid",
            // 前置条件：属性 balance >= 1，且至少有 1 个 items 邻居
            Preconditions: []lifecycle.Precondition{
                {Attr: &lifecycle.AttrCheck{Key: "balance", Op: lifecycle.CmpGe, Value: int64(1)}},
                {LinkCount: &lifecycle.LinkCountCheck{Link: "items", Min: 1, Max: -1}},
            },
            // 属性校验钩子（迁移后）
            AttrBounds: []lifecycle.AttrBound{
                {Key: "balance", Min: ptr(int64(1))},
            },
            // 迁移后链接基数上限
            Cardinality: []lifecycle.CardinalityBound{
                {Link: "items", Min: 0, Max: 100},
            },
            // 跨实例钩子：所有 charged-to 邻居必须处于 active
            Hooks: []lifecycle.Hook{
                {Link: "charged-to", RequireStates: []lifecycle.State{"active"}},
            },
            // 链式触发：付款后级联通知 notify 邻居执行 "activate"
            Cascades: []lifecycle.Cascade{
                {Link: "notify", ToRule: "activate"},
            },
            MutexGroup: "billing", // 同组迁移在一个单元内互斥
        },
    },
})
```

前置条件三选一：

- `Attr`：属性比较（`eq/ne/lt/le/gt/ge`）；
- `LinkCount`：经由某链接类型的出边数量区间（`Min/Max`，-1 表示不限制该端）；
- `LinkState`：邻居状态，`RequireAll=true` 要求全部满足，否则要求至少一个。

## 创建实例、属性与链接

```go
eng := lifecycle.NewEngine(store, lifecycle.NewWriterLogger(os.Stdout))
store.CreateInstance("o1", "Order")

eng.SetAttrs("o1", lifecycle.AttrOp{Key: "balance", Op: lifecycle.AttrSet, Value: int64(1)})
eng.ModifyLinks("o1", lifecycle.LinkOp{Link: "items", Target: "sku-9", Op: lifecycle.LinkAdd})
```

`SetAttrs` / `ModifyLinks` 自身也是处理单元：终态实例改属性或新增链接会被
`ErrTerminal` 拒绝，但终态实例删除已有链接允许。

## 触发迁移（可批量，构成一个处理单元）

```go
outs := eng.Execute(
    lifecycle.TransitionRequest{Instance: "o1", Rule: "pay", Priority: 0,
        // 可携带随本单元生效的属性/链接变更
        Attrs: []lifecycle.AttrOp{{Key: "note", Op: lifecycle.AttrSet, Value: "vip"}},
        Links: []lifecycle.LinkOp{{Link: "items", Target: "sku-10", Op: lifecycle.LinkAdd}},
    },
    lifecycle.TransitionRequest{Instance: "o2", Rule: "pay", Priority: 1},
)
for _, o := range outs {
    if o.Err != nil {
        // o.Err.Code 为固定错误码；o.Err.Code.Name() 为可读名称
    }
}
```

- `Priority` 数值小者优先，决定同实例互斥汇聚时的放行方，不依赖到达顺序；
- 批次内的链式触发整体生效或整体不生效；
- 被拒请求不改变任何状态/属性/链接/时钟。

## 读取状态

```go
inst, ok := store.SnapshotInstance("o1") // 深拷贝快照
inst.State                             // 当前状态
inst.Attrs["balance"]                  // 属性
inst.Clock                             // 最近一次生效处理单元的时钟
store.Neighbors("o1", "items")         // 出边邻居（稳定排序）
```

## 日志

- `lifecycle.NewWriterLogger(w io.Writer)`：结构化打印每次迁移；
- `lifecycle.NewMemoryLogger()`：在内存中保留 `LogEntry`，供测试断言；
- `lifecycle.NopLogger()`：关闭日志。

每行包含：序号、目标、规则、优先级、状态迁移、携带的属性/链接变更、
`ACCEPT`/`REJECT(错误名)` 以及逐项判定依据（前置/迁移后基数/钩子结论）。

## 与朴素模型对拍

`lifecycle/naive` 提供一份全局加锁、副本模拟的独立实现，类型声明字段同名
但独立定义。`lifecycle/lifecycle_diff_run_test.go` 演示了如何用相同的随机
请求序列驱动两个模型并断言错误码、状态、属性与链接完全一致。

## 错误码优先级

数值越小优先级越高：
`Undeclared(1) < Precondition(2) < Mutex(3) < Cardinality(4) < Hook(5) <
Cycle(6) < Terminal(7)`。多个条件同时满足时报告优先级最高的一类。
