# 使用指南

所有 API 位于包 `ontology/layerconfig`，门面类型为 `*layerconfig.Store`。

## 1. 登记键模式

键必须先登记后使用。模式包含：值类型、是否必填、整数范围、合并方式。

```go
s := layerconfig.NewStore()

_ = s.RegisterKey(layerconfig.Schema{
    Key:      "timeout_ms",
    Type:     layerconfig.TypeInt,
    Required: true,
    Min:      0,
    Max:      1_000_000,
    Merge:    layerconfig.MergeOverride, // 标量只能 Override
})

_ = s.RegisterKey(layerconfig.Schema{
    Key:   "features",
    Type:  layerconfig.TypeStringList,
    Merge: layerconfig.MergeAppend,    // 列表可选 Override / Append
})
```

类型：`TypeString` / `TypeInt` / `TypeBool` / `TypeStringList`。
合并：`MergeOverride`（窄覆盖宽）、`MergeAppend`（仅列表，宽到窄拼接去重）。
模式不版本化，登记始终以最新模式生效（含历史读取）。

## 2. 发布

一次发布是一批 `Change`，全有或全无：校验通过才产生新版本（从 0 开始递增），
失败不改任何状态与版本。

```go
v, err := s.Publish([]layerconfig.Change{
    {Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "timeout_ms",
        Value: layerconfig.Value{Int: 1000}},
    {Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod"}, Key: "timeout_ms",
        Value: layerconfig.Value{Int: 250}},
    {Op: layerconfig.OpWrite, Scope: layerconfig.Scope{Env: "prod", Region: "cn", Instance: "canary"},
        Key: "features", Value: layerconfig.Value{List: []string{"canary"}}},
})
```

操作：`OpWrite`（写值）、`OpCancel`（显式取消，作废本层及更宽层累积）、
`OpClear`（清除本层该键的写入/取消标记）、`OpLock`、`OpUnlock`。

`Scope` 合法性：

- 全局：三个名字都为空；
- 环境：仅 `Env` 非空；
- 区域：`Env`、`Region` 非空，`Instance` 空；
- 实例：三者都非空。

## 3. 解析

```go
// 当前版本（version 传负数）
r, err := s.Resolve(-1, layerconfig.Scope{Env: "prod", Region: "cn"}, "features")
if err != nil { /* 分类见下 */ }
if !r.Present {
    // 未设置：区别于空串、空列表
}
// r.Value.List / .Str / .Int / .Bool

// 指定历史版本（0 = 初始空版本）
old, err := s.Resolve(3, layerconfig.Scope{Env: "prod"}, "timeout_ms")

// 一次解析多个键
vals, err := s.ResolveView(-1, target, []string{"timeout_ms", "features"})
```

合并语义：

- 覆盖：最窄的存活值生效；
- 追加：从宽到窄拼接，重复元素保留最早出现位置；
- 取消：清空截至该层的累积（标量与列表皆然），更窄层写入照常叠加。

## 4. 锁定

```go
_, _ = s.Publish([]layerconfig.Change{
    {Op: layerconfig.OpLock, Scope: layerconfig.Scope{}, Key: "timeout_ms"},
})
```

上锁只约束更窄层；上锁层本身可继续写。上锁时若更窄层已有该键写入，发布被拒，
必须先用 `OpClear` 清理。环境/区域锁只作用于同一前缀链。`OpUnlock` 解除。

## 5. 回滚

```go
nv, err := s.Rollback(3) // 产生新版本，内容与 v3 完全一致
_, _ = s.Rollback(s.CurrentVersion()) // 回滚到当前 = 无操作，不产生新版本
```

历史版本不删除；回滚后若最新模式（例如新增必填键）使旧内容不满足必填校验，
回滚会返回 `RequiredMissing`，否则总是成功。

## 6. 错误分类

所有错误都是 `*layerconfig.Error`，按优先级（高到低）：

1. `KindInvalidArgument`
2. `KindVersionNotFound`
3. `KindKeyNotRegistered`
4. `KindTypeOrRange`
5. `KindLockConflict`
6. `KindBatchConflict`
7. `KindRequiredMissing`

```go
if e, ok := layerconfig.AsError(err); ok {
    switch e.Kind {
    case layerconfig.KindLockConflict:
        // ...
    }
}
```

## 7. 并发

`Store` 可被多 goroutine 并发使用：发布/回滚/登记互斥串行，解析读锁并行；
读者读到的永远是某个完整版本，不会看到半次发布。
