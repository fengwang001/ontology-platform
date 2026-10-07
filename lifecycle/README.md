# lifecycle — 对象生命周期状态机子系统

每个对象类型声明自己的状态集合、终态与允许的有向迁移；实例的状态迁移
只能由动作触发。一次迁移的前置条件、迁移后链接基数、跨实例校验钩子、
级联随迁在**同一个处理单元**内协同求值：全部满足才整体生效，任一失败
则整体不生效，不留下任何可观察的中间态。

## 包结构

- `lifecycle/`：生产实现。
  - `model.go` 类型/迁移/前置条件/基数/钩子/级联声明；
  - `runtime.go`、`store.go`、`snap.go` COW 版本存储与加锁协议；
  - `workview.go` 规划期私有工作视图（稀疏 overlay + 属主邻接）；
  - `planner.go` 前置条件、互斥、级联 DFS、循环预检、迁移后基数/钩子；
  - `eval.go` 前置条件与迁移后校验的逐条求值；
  - `engine.go`、`commit.go` 处理单元、乐观重试、COW 提交、日志；
  - `errors.go` 七类错误与固定优先级。
- `internal/naive/`：独立的朴素逐步推进参考实现（对拍基线）。
- `cmd/demo/`：可运行示例（打印每次迁移的输入、判定依据、结果）。
- `docs/design.md`：设计说明（取舍、放弃方案、复杂度论证、验证方法）。

## 快速使用

```go
schema := &lifecycle.Schema{Types: map[string]*lifecycle.ObjectType{
    "order": {
        States:      []string{"created", "paid", "closed"},
        FinalStates: map[string]bool{"closed": true},
        Transitions: map[string]*lifecycle.Transition{
            "pay": {
                From: "created", To: "paid",
                Preconds: []lifecycle.Precondition{lifecycle.Attr("approved", true)},
                MaxCard:  []lifecycle.CardinalityRule{{LinkType: "tag", Max: 2}},
                Hooks:    []lifecycle.HookRule{{LinkType: "invoice",
                    States: []string{"issued"}}},
                Cascades: []lifecycle.CascadeRule{{
                    LinkType: "invoice", WhenStates: []string{"pending"},
                    Transition: "issue",
                }},
            },
        },
    },
}}

store := lifecycle.NewStore()
store.AddInstance(&lifecycle.Instance{ID: "o1", Type: "order", State: "created"})
eng := lifecycle.NewEngine(schema, store, lifecycle.NewTextLogger(os.Stdout))

// 单个处理单元：新增链接 + 触发迁移，要么整体生效，要么整体不生效。
res, _ := eng.Batch([]lifecycle.Op{
    lifecycle.AddLink("invoice", "o1", "i1"),
    lifecycle.Fire("o1", "pay"),
})
```

## 语义要点

- **原子单元**：`Batch` 内任一操作被拒绝，整批回滚；被拒操作不推进
  逻辑时钟，也不改任何实例的状态、属性、链接或时间戳。
- **迁移后校验**：基数与钩子在迁移（含整棵级联树）生效后的视图上求值，
  同批新增链接计入基数。
- **链式触发**：先在私有视图完整推演并检测循环，再一次性提交；
  循环检测不依赖任何计数上限。
- **终态**：禁止迁移/改属性/参与新增链接，但允许删除已有链接。
- **互斥**：同处理单元内互斥组按调用方声明顺序先占先得，与到达时间无关。
- **并发**：实例锁按 ID 字典序获取（无死锁）；实例集合不相交的处理单元
  完全并行，同一实例的并发经版本指针 + 乐观重试串行化，结果等价于某个
  合法串行序。
- **复杂度**：判定只访问本次涉及的实例与链接，不遍历历史迁移记录。

## 错误优先级（高 → 低）

`Unknown`（规则未声明） > `Precondition` > `Mutex` > `Cardinality`
> `Hook` > `Cycle` > `Terminal`。

## 测试与对拍

```bash
go test -race ./...
go test -run TestDifferential -v ./lifecycle   # 随机对拍（含决策日志）
go run ./cmd/demo
```

随机对拍使用与生产实现零代码共享的朴素模型 `internal/naive`
（单锁、逐步生效 + 逆操作回滚、递归级联），比较每个操作后的
状态、属性、链接与拒绝类别；并发用例另与朴素模型的随机串行序对齐。
