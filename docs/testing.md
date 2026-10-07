# 测试矩阵与验证说明

全部测试位于 `ontology/`，与被测代码同包（白盒）。

| 测试 | 文件 | 覆盖的需求点 |
|---|---|---|
| `TestOutOfOrderExhaustive` | `ordering_test.go` | 同一对象同一属性 3 条变更的全部 6 种到达排列；最终值恒为逻辑顺序最终值，中间值不固化 |
| `TestDuplicateDeliveryExhaustive` | `ordering_test.go` | 每条事件 3 份重复 + 50 种随机洗牌；幂等去重计数审计 |
| `TestTieBrokenByEventID` | `ordering_test.go` | 相同 `EffectiveAt` 时按 `EventID` 决胜 |
| `TestLateArrivalRewindsValue` | `ordering_test.go` | 低时间戳迟到事件不得覆盖高时间戳 |
| `TestCrossCutoverAttribution` | `switch_test.go` | 四种跨切换点到达组合（提前到的新字段、窗口内旧/新、提交后迟到旧字段）归属；切换中查询 E4；新版本无旧值泄漏；历史化审计 |
| `TestSameObjectChangesAcrossSwitch` | `switch_test.go` | 同一对象跨切换连续变更，最终只反映新字段最终值 |
| `TestSwitchValidationFailureRollback` | `switch_test.go` | E2 唯一约束冲突 → 整体回滚；缓冲事件重归入旧版本不丢；修复后可再次切换成功 |
| `TestAbortSwitchReintegratesBuffered` | `switch_test.go` | 显式中止后缓冲事件在旧版本生效 |
| `TestDeprecatedPropertyNoReplacement` | `switch_test.go` | E1：废弃无替代字段的写入与查询 |
| `TestPropertyUndefined` | `switch_test.go` | E3：属性在生效版本中尚未定义 / 之后已定义 |
| `TestUnknownTypeReportsUndefined` | `switch_test.go` | 类型首个版本生效之前的 E3 报告 |
| `TestRandomDifferentialNoSwitch` | `diff_test.go` | 40 个随机种子：乱序+重复事件流，引擎视图逐条对照朴素批量重建；审计记录完备性 |
| `TestRandomDifferentialWithSwitch` | `diff_test.go` | 30 个随机种子：随机位置穿插一次成功切换，新版本对照朴素 `switched=true` 重建 |
| `TestRandomDifferentialFailedSwitchRollback` | `diff_test.go` | 随机数据下 E2 回滚，旧版本对照朴素全量重建 |
| `TestConcurrentSerializability` | `concurrent_test.go` | 8 写者 + 4 读者 + 切换并发（`-race`）；最终视图等价串行；提交后不存在依据旧版本的返回 |
| `TestJSONLAuditorPersists` | `audit_test.go` | 判定记录 JSONL 落盘，事后逐条可核查 |
| `TestLookupConstantTime` | `benchmark_test.go` | 1 万 vs 100 万累计事件下查询耗时比值断言（默认跳过，`ONT_RUN_SLOW=1` 启用） |
| `BenchmarkLookup{1k,10k,100k,1m}` | `benchmark_test.go` | 四档查询基准（命中桶含多对象，体现结果拷贝成本） |
| `BenchmarkLookupSingleHit{1k,100k,1m}` | `benchmark_test.go` | 单命中桶基准：1k→1m 事件下 ns/op 与 B/op 恒定，直接证明定位 O(1) |

## 朴素批量重建模型（oracle）

`NaiveModel`（`naive.go`）刻意不做任何增量：保存去重后的原始事件，
每次对照都从空索引开始，按 `(EffectiveAt, EventID)` 排序重放：

- `Rebuild(id, switched=false, _)`：所有事件取每对象最后值 → 期望旧版本；
- `Rebuild(id, switched=true, cutoverAt)`：只取 `ts >= cutoverAt` →
  期望新版本，并返回唯一约束是否满足（用于对照 E2）。

差分测试把同一随机事件流同时喂给 `Engine` 与 `NaiveModel`，在每个
检查点比较完整的 `value -> []objectID` 视图，任何分歧立即失败。

## 审计记录

每条判定都通过 `Auditor` 记录 `AuditRecord`：

- 输入：`EventID / ArrivedAt / EffectiveAt`；
- 依据：`IndexID / IndexVersion`；
- 结论：`Decision`（如 `accepted / duplicate_ignored /
  buffered_during_switch / accepted_historical_only /
  rolled_back_validation_failed / returned / rejected_*`）；
- 错误：`ErrorCode`；`Detail` 附切换点、对象数等。

`MemoryAuditor.Snapshot()` 供测试断言；`JSONLAuditor` 逐行追加 JSON。
