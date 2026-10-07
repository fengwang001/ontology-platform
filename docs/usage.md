# 使用指南

## 快速开始

```go
import "ontology/ontology"

// 1) 声明对象类型：阶段、允许的直接转移（可有环/自环）、终态。
ot, err := ontology.NewObjectType("order",
    []string{"draft", "review", "done"},
    [][2]string{
        {"draft", "review"}, {"review", "draft"}, // 环
        {"review", "done"}, {"draft", "draft"},   // 自环
    },
    []string{"done"}, // 终态
)

// 2) 注册两类钩子。
reg := ot.Registry()
reg.RegisterTransition("draft", "review", ontology.Hook{
    ID:       "check-review",
    Semantic: ontology.CommitImmediately, // 或 CommitOnSuccess + Compensate
    Check: func(c *ontology.TransitionContext) error {
        // 返回非 nil 即拒绝转移（错误码 hook_failed）。
        return nil
    },
})
reg.RegisterEntry("review", ontology.Hook{
    ID: "on-enter-review",
    Check: func(c *ontology.TransitionContext) error { return nil },
})

// 3) 创建实例并发起转移；可安全地从多 goroutine 并发调用。
mgr := ontology.NewManager()
_, _ = mgr.CreateInstance(ot, "o-1", "draft")
res, err := mgr.Transition("o-1", "review")
```

## 错误处理

```go
if le, ok := ontology.AsLifecycleError(err); ok {
    switch le.Code {
    case ontology.ErrorInvalidArgument:      // 实例不存在 / 目标阶段未声明
    case ontology.ErrorTerminal:             // 起始阶段为终态，钩子未被执行
    case ontology.ErrorTransitionNotAllowed: // 关系未声明（含未声明的自转移）
    case ontology.ErrorHookFailed:           // le.HookID 为首个失败钩子
    }
}
```

拒绝原因按上述顺序只报第一个；被拒绝的转移不改变 `Current()` 与 `History()`，
`FireLog()` 中可看到已触发钩子及其补偿记录（`Compensated=true`）。

## 提交语义

- `CommitImmediately`：副作用（如已外发日志）一经产生即落定，转移失败不撤销。
- `CommitOnSuccess`：转移失败时按触发逆序调用 `Compensate` 撤销。

## 触发顺序

具体转移钩子（按注册序）→ 目标阶段进入钩子（按注册序）。
自转移 `(s,s)`：进入钩子恒触发；具体转移钩子仅在显式注册 `RegisterTransition(s,s,...)` 时触发。
