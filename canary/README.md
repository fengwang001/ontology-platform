# canary：带指标闸门与自动回滚的灰度流量切分器

把请求在稳定版本与灰度版本之间按"万分之一"比例切分，随阶段推进逐步放量；
每阶段驻留满 `D` 毫秒且灰度样本不少于 `G` 后，按灰度/稳定错误率判定晋级，
连续失败 `F` 次自动回滚。时钟完全由调用方注入。

## 快速开始

```go
import "ontology/canary"

s, err := canary.New(canary.Config{
    Stages:              []int{500, 2000, 5000, 10000}, // 5%, 20%, 50%, 100%
    MinDwell:            60_000,   // D：每阶段至少驻留 60s
    MinGrayRequests:     1000,     // G：评估至少 1000 个灰度请求
    ErrorRateTolerance:  100,      // T：允许灰度错误率高 1 个百分点
    MaxConsecutiveFails: 3,        // F：连续 3 次失败即回滚
    StickyTTL:           30_000,   // L：会话粘性 30s（左闭右开）
    MaxSticky:           1_000_000,// P：粘性记录上限（LRU 淘汰）
})
if err != nil { /* 参数非法 */ }

if err := s.Start(nowMs); err != nil { /* 时钟回退或状态不允许 */ }

r, err := s.Route(userID, nowMs)
switch r.Version {
case canary.VersionGray:   // 走灰度
case canary.VersionStable: // 走稳定
}
// r.Source: SourceRollback > SourceSticky > SourceRatio

// 业务侧拿到请求结果后回传
_ = s.Observe(r.Version, success, nowMs)

// 周期性触发闸门
res, _ := s.Evaluate(nowMs)
switch res.Outcome {
case canary.EvalDwellNotMet:        // 驻留未满
case canary.EvalInsufficientSamples:// 样本不足（G-1 也不会累计失败）
case canary.EvalPassed:             // 晋级；末阶段通过则已完成
case canary.EvalFailed:             // 失败；res.RolledBack 表示已自动回滚
}
```

人工操作：`Start(now)`（仅未开始）、`Demote(now)`（进行中且非首阶段，
保粘性、重计驻留）、`Reset(now)`（任意状态，清空配置外一切）。

## 语义要点

- 归属稳定：`canary.Placement(id)` ∈ [0,10000)，对同一 id 恒定；
  灰度条件为 `Placement(id) < 当前比例`，比例只增时 id 不会从灰度退回稳定。
- 粘性优先于比例，回滚强制稳定优先于一切；进入回滚会清空粘性，
  回滚态路由不读不写粘性。
- 错误率比较是整数精确比较（无浮点），灰度错误率恰等"稳定 + 容差"判通过。
- 错误固定优先级：参数非法 → 时钟回退 → 状态不允许；被拒操作不改任何状态。
- 路由/观测 O(1)（淘汰与粘性上限 P 相关，与历史请求总量无关）；
  全方法互斥，并发等价于串行。

更多取舍与被放弃的方案见 [DESIGN.md](DESIGN.md)。

## 测试

```bash
go test -v ./canary/      # 1200 组随机事件序列对照朴素模型，逐步打印日志
go test -race ./canary/
```

定向用例覆盖：驻留 D-1/D、样本 G-1/G、错误率恰等边界、失败累计被通过清零、
比例递增不回退、降级保粘性、回滚清粘性、粘性 L-1/L 过期与 LRU 淘汰、
拒绝次序、并发安全，以及历史 100 万时路由耗时不增长的可验证证明。
