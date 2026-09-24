# NOTES — 变更流确定性重放 + LWW 压缩

## 七步分步表（每步到达后 a/b/c 的 winner：`Ver/Val`，无=该键尚无变更）

| 步 | sn | a | b | c |
|---|---|---|---|---|
| 1 | 1 | 10/100 | 无 | 无 |
| 2 | 2 | 10/100 | 5/50 | 无 |
| 3 | 3 | 10/**200** | 5/50 | 无 |
| 4 | 4 | 10/200 | 5/50 | 7/70 |
| 5 | 5 | 10/200 | 5/50 | 7/70 |
| 6 | 6 | 10/200 | 12/**90** | 7/70 |
| 7 | 7 | 10/200 | 12/90 | 7/**77** |

- **(甲)** sn1 与 sn3 同为 `Ver=10`，sn3 后到 → 最终 `a=200`。若错成「并列取先到达者」→ `a=100`。
- **(乙)** 第 5 步 `Ver=5 < 10`，被忽略 → 最终 `a=200`。若错按「sn 最大者胜」→ 取 sn5 → `a=50`。
- **(丙)** 最终 winner：`a=200、b=90、c=77`；按 Key 字典序 → **a,b,c**。若错按 Ver 降序 → b(12),a(10),c(7)。

## 四条不变量：代码保证位置 / 钉住的测试函数

1. **与朴素参照一致**：winner 判定在 `lww/lww.go` 的 `Table.Apply`，Key 升序输出在 `Table.Snapshot`；钉住：`TestReplayMatchesReference`（api/api_test.go）。
2. **幂等确定性**：`Snapshot` 只排序、新建切片返回，不写 winner；`api.Engine.Replay/History` 只持 RLock 且返回拷贝（api/api.go）；钉住：`TestReplayDeterministic`、`TestConcurrentReadsEqual`。
3. **winner 稳定**：`Apply` 的「Ver 优先、同 Ver 取 sn 大者」（lww/lww.go）；钉住：`TestApplyWinner`（lww/lww_test.go）与 `TestWinnerStepwise`（api/api_test.go）。
4. **失败不留痕**：`Feed` 先整批校验（空 Key/负 Ver/超限计数），全部通过后才在提交阶段分配 sn（api/api.go）；钉住：`TestFeedRejectionAtomic`。

计数有界性（Replay 不重扫历史）由非导出字段 `lww.Table.checked` 记录、`TestCheckedCounterBoundedInM` 钉住，仅包内测试可读。
