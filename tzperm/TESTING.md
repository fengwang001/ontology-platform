# 测试说明

所有命令在仓库根目录执行，本机 Go 位于 `/usr/local/go/bin`：

```bash
export PATH=$PATH:/usr/local/go/bin
go test -race -vet=all ./...
```

## 覆盖矩阵

| 需求点 | 测试 |
| --- | --- |
| gap（春季跳过）确定性前移 | `TestResolveWallSpringForwardGap` |
| overlap（秋季重复）取较早时刻 | `TestResolveWallFallBackOverlap` |
| 偏移分段与负时间本地日 | `TestOffsetAtSegments`、`TestSecondsOfDayNegative` |
| 半开边界、跨午夜、全天 | `TestWindowHalfOpenBoundaries`、`TestWindowInvalidEndpoints` |
| 基准恒为地区时区（非查询者/录入时区） | `TestBaselineIsRegionZoneNotQuerierOrEntry` |
| 历史值在迁移后结论不摇摆 | `TestHistoricalValueUsesBaselineAtValueInstant` |
| DST 切换日半开边界无特例 | `TestWindowBoundaryHalfOpenAcrossDSTDay` |
| 四种错误与优先级单选 | `TestErrorPriorityReportsOneCode` |
| 拒绝/错误结果不泄露 | `TestOutcomeLeakFree`（反射枚举全部导出字段） |
| 三时区 × DST × 版本迁移穷举 | `TestExhaustiveThreeSourcesDSTMigration`（432 组合 × 重复判定） |
| 与朴素逐条模型随机对照 | `TestRandomizedAgainstNaive`（3000 次含规则/模式/地区变更） |
| 成本不随版本数线性增长 | `TestLookupCostNotLinear`、`TestEndToEndComparisonCounts`、`BenchmarkCheckVsVersionCount` |
| 并发安全性（线性一致性的经验佐证） | `TestConcurrentLinearizableSafety` |
| 审计记录输入/基准/对照结论 | 上述端到端测试均断言 `MemorySink.Records()` 字段 |

## 朴素对照模型

`tzperm/tznaive` 是独立的参考实现：不假设版本切片已排序、线性扫描、
自行重写窗口与秒数算法，只复用时区切换算术。随机测试逐条比对
`(Decision, ErrorCode)`，分歧即失败并打印双方比较计数与请求。
