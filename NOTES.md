# NOTES — 保留首条去重，九步推导（同一 Key=k，记号 a5 = (ID=a,T=5)，输出省略 k）

| 步 | 存活行，按 (T,ID) 升序 | 本步输出（按序） | 视图 k |
|---|---|---|---|
| 1 +a5 | a5 | +a5 | a5 |
| 2 +b8 | a5,b8 | 无 | a5 |
| 3 +p3 | p3,a5,b8 | -a5,+p3 | p3 |
| 4 +m3 | m3,p3,a5,b8 | -p3,+m3 | m3 |
| 5 -m | p3,a5,b8 | -m3,+p3 | p3 |
| 6 +e1 | e1,p3,a5,b8 | -p3,+e1 | e1 |
| 7 -b | e1,p3,a5 | 无 | e1 |
| 8 -e | p3,a5 | -e1,+p3 | p3 |
| 9 -p | a5 | -p3,+a5 | a5 |

(甲) 只存首条者第 5 步只输出 -m3（b/p/a 的排序键早已丢弃，无法补位）；视图错成「空」，正确应为 p3（应再输出 +p3）。
(乙) 先到先得者第 6 步输出「无」（首条 a 仍存活，更早 T 的 e 不能顶替）；视图错成 a5，正确应为 -a5,+e1、视图 e1。
(丙) T 相等按到达先后：第 4 步输出「无」（p 先到，继续居首），视图仍为 p3；自第 5 步起两实现视图重新一致（均为 p3，之后各步相同）。九步条目总数：按 ID 字典序 13，按到达先后 9。

## 四条不变量：保证位置 / 钉住的测试

1. 与批量重算一致：dedup.go `Engine.View` 遍历各 group 的 `group.first`，而 `group.emit` 仅在 `rank.Set.Min` 变化时换首条；TestRandomViewMatchesBatch、TestSelfCheckBuiltins。
2. 变更日志自洽：dedup.go `group.emit` 固定「先 -旧 Min 再 +新 Min」，rank 保证每 Key 全存活集有序；TestLogPrefixes。
3. 输出最小：dedup.go `group.emit` 首条不变时返回 nil，每条输入至多产出 2 条；TestOutputMinimal、TestNineStepGolden。
4. 失败不留痕：dedup.go `Engine.Commit` 先整批模拟校验（ErrInvalidChange/ErrIDExists/ErrIDMissing/ErrRowLimit 四类互异）全部通过后才提交，api.go `Apply` 仅加锁委托；TestRejectedBatchAtomicity。
