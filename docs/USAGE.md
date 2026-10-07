# 使用指南

## 结果码（互斥、可单独识别）

| Outcome | 含义 |
| --- | --- |
| `OutcomeCommitted` | 成功 |
| `OutcomeOccupied` | 占用申请被拒绝：单实例已被另一动作占用 |
| `OutcomeLockConflict` | 跨实例占用申请被确定性规则拒绝 |
| `OutcomeOptimisticRejected` | 占用期间的普通乐观更新被拒绝（版本不变） |
| `OutcomeVersionStale` | 乐观更新版本落后 |
| `OutcomeCardinality` | 链接基数冲突 |
| `OutcomeInvalidLease` | 占用权失效（过期 / 已终结 / 僵尸调用） |

## 普通乐观更新

```go
store := ontology.NewStore(nil) // nil 使用墙上时间时钟
store.CreateInstance("order-1", "open", ontology.Props{"amount": "10"})

snap, _ := store.Snapshot("order-1")
switch store.Update("svc-a", "order-1", snap.Version, ontology.Patch{"amount": "12"}) {
case ontology.OutcomeCommitted:
    // 成功，版本已 +1
case ontology.OutcomeOptimisticRejected:
    // 实例正被动作独占；不要排队，稍后重读最新版本再试
case ontology.OutcomeVersionStale:
    // 有并发提交；重读快照后用新版本重试
}
```

## 动作触发的状态机转换

```go
lease, out := store.TryAcquire("action-ship", []string{"order-1"}, 5_000) // TTL 5s
if out != ontology.OutcomeCommitted {
    // out == OutcomeOccupied：立即收到拒绝，不阻塞、不自动排队
    return
}

// 长任务期间周期续约；崩溃且停止续约时，TTL 后占用被唯一裁定为失效
ticker := time.NewTicker(time.Second)
defer ticker.Stop()
go func() { for range ticker.C { lease.Heartbeat() } }()

lease.Stage("order-1", "shipped", ontology.Props{"amount": "12"}, nil) // 暂存，外部不可见
if err := doSideEffect(); err != nil {
    lease.Release() // 放弃：不发布任何改动
    return
}
lease.Commit() // 原子发布全部暂存改动 + 版本 +1 + 释放占用
```

## 跨链接的多实例动作

```go
// 键的给出顺序无关：系统按全局规范序在单一步骤内原子授予或整体拒绝。
lease, out := store.TryAcquire("action-transfer", []string{"acct-B", "acct-A"}, 5_000)
// 冲突时 out == ontology.OutcomeLockConflict；决策日志记录 ConflictKey/Holder。
```

## 判定日志与重放

```go
for _, d := range store.Log().Entries() {
    raw, _ := json.Marshal(d) // Seq/Kind/Caller/Keys/BaseVer/Outcome/Reason/...
    _ = raw
}
```

每条记录含输入（调用方、键、基准版本、TTL、暂存负载）、判定依据
（`Holder`、`ConflictKey`、`At`、`Token`）与结果（`Outcome`、`NewVersion`），
足以脱离进程重放整条历史。

## 链接基数

```go
store.AddLinkType(ontology.LinkType{Name: "owns", MaxOut: 2})
store.Link("svc", "person-1", baseVersion, "owns", "car-1")
// 超限返回 OutcomeCardinality；占用/版本判定先于基数判定。
```
