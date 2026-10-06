# layercfg

四层（全局 / 环境 / 区域 / 实例）分层配置的版本化发布与解析库。支持多层
覆盖与追加、显式取消、层级锁定、原子批量发布、历史回滚、按版本读取。

## 快速开始

```go
package main

import (
    "fmt"
    "ontology/layercfg"
)

func main() {
    store := layercfg.NewStore() // 当前版本 0，配置为空

    // 1) 登记键模式
    _ = store.RegisterKey(layercfg.SchemaSpec{
        Key:      "timeout_ms",
        Type:     layercfg.TypeInt,
        Required: true,
        Range:    &layercfg.IntRange{Min: 0, Max: 60000},
    })
    _ = store.RegisterKey(layercfg.SchemaSpec{
        Key:   "features",
        Type:  layercfg.TypeStringList,
        Merge: layercfg.MergeAppend,
    })

    // 2) 一次原子发布（多条变更，全有或全无）
    v, err := store.Publish([]layercfg.Change{
        {Op: layercfg.OpSetValue, Ref: layercfg.Ref{Layer: layercfg.LayerGlobal},
            Key: "timeout_ms", Value: layercfg.NewInt(1000)},
        {Op: layercfg.OpSetValue,
            Ref: layercfg.Ref{Layer: layercfg.LayerEnv, Env: "prod"},
            Key: "timeout_ms", Value: layercfg.NewInt(2000)},
        {Op: layercfg.OpSetValue,
            Ref: layercfg.Ref{Layer: layercfg.LayerRegion, Env: "prod", Region: "cn"},
            Key: "features", Value: layercfg.NewStringList([]string{"beta", "dark"})},
    })
    fmt.Println("published version:", v, err)

    // 3) 解析（第三个参数为版本号，-1 表示当前版本）
    r, _ := store.Resolve("timeout_ms",
        layercfg.Scope{Env: "prod", Region: "cn", Instance: "i-1"}, -1)
    if r.Present {
        fmt.Println("timeout_ms =", r.Value.Int) // 2000
    }

    all, _ := store.ResolveAll(layercfg.Scope{Env: "prod", Region: "cn"}, -1)
    fmt.Println("features =", all["features"].Value.List) // [beta dark]

    // 4) 取消、清除、锁定、解锁
    // store.Publish([]layercfg.Change{{Op: layercfg.OpCancel,
    //     Ref: layercfg.Ref{Layer: layercfg.LayerEnv, Env: "prod"}, Key: "timeout_ms"}})
    // store.Publish([]layercfg.Change{{Op: layercfg.OpLock,
    //     Ref: layercfg.Ref{Layer: layercfg.LayerGlobal}, Key: "timeout_ms"}})

    // 5) 回滚：产生内容与历史版本完全相同的新版本；回滚当前版本无操作
    nv, err := store.Rollback(1)
    fmt.Println("rolled back to new version:", nv, err)
}
```

## API 摘要

- 层：`LayerGlobal`、`LayerEnv`、`LayerRegion`、`LayerInstance`；层定位用
  `Ref{Layer, Env, Region, Instance}`，各层限定名必须逐级带齐。
- 解析三元组 `Scope{Env, Region, Instance}`：区域/实例可缺省，缺省表示只
  解析到对应层；窄限定必须逐级带齐父限定，否则返回 `ErrInvalidArgument`。
- 值类型：`TypeString`、`TypeInt`、`TypeBool`、`TypeStringList`；
  用 `NewString/NewInt/NewBool/NewStringList` 构造。
- 合并方式：标量只能 `MergeReplace`；字符串列表可选 `MergeAppend`
  （从宽到窄拼接、重复元素保留首次出现位置）。
- 变更操作：`OpSetValue`、`OpCancel`（显式取消，作废本层及更宽累积，
  窄层仍可再写）、`OpClearWrite`（清除该层值槽）、`OpLock`、`OpUnlock`。
- 读取：`Resolve(key, scope, version)`、`ResolveAll(scope, version)`；
  `version < 0` 为当前版本。`Result.Present=false` 表示未设置，与空串/
  空列表严格区分。
- 错误：`*layercfg.Error`，其 `Kind` 为可分支处理的错误类别。

## 错误类别优先级（高 → 低）

`ErrInvalidArgument` > `ErrVersionNotFound` > `ErrKeyNotRegistered` >
`ErrTypeOrRange` > `ErrLockConflict` > `ErrConflict` > `ErrRequiredMissing`。
一次操作命中多个问题时返回优先级最高者；被拒绝的操作不改变任何状态与版本。

设计原理、被放弃方案与性能/正确性的可复现验证见 `docs/design.md`。
