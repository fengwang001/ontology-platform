# NOTES：确定性重放 + LWW 压缩

## 一、七步推导（规则：sn 按到达顺序 1 起连续；winner = Ver 最大，并列取 sn 大者）

| 步 | 到达 (Key,Ver,Val) | sn | a winner (Ver,Val) | b winner (Ver,Val) | c winner (Ver,Val) |
|---|---|---|---|---|---|
| 1 | (a,10,100) | 1 | (10,100) | 无 | 无 |
| 2 | (b,5,50) | 2 | (10,100) | (5,50) | 无 |
| 3 | (a,10,200) | 3 | (10,200) ← Ver 并列，sn=3 > sn=1，后到者胜 | (5,50) | 无 |
| 4 | (c,7,70) | 4 | (10,200) | (5,50) | (7,70) |
| 5 | (a,5,50) | 5 | (10,200) ← Ver=5 < 10，忽略 | (5,50) | (7,70) |
| 6 | (b,12,90) | 6 | (10,200) | (12,90) ← Ver=12 > 5 | (7,70) |
| 7 | (c,7,77) | 7 | (10,200) | (12,90) | (7,77) ← Ver 并列，sn=7 胜 |

- **(甲)** 最终 a 的 Val = **200**。若「并列取先到达者」，会错成 **100**（sn=1 的 (a,10,100)）。
- **(乙)** 最终 a 的 Val = **200**。若错按「sn 最大者胜」，会错成 **50**（sn=5 的 (a,5,50)）。
- **(丙)** 三键 winner：a=(10,200)、b=(12,90)、c=(7,77)；正确输出按 Key 升序：**a→b→c，即 (a,200),(b,90),(c,77)**。若错按 Ver 降序输出，顺序会错成 **b(12)→a(10)→c(7)，即 (b,90),(a,200),(c,77)**。

## 二、四条不变量的保证位置与钉住测试

1. **与朴素参照一致**：`lww/lww.go` `Apply` 增量维护 winner（Ver 大者胜、并列 sn 大者胜），`Replay` 对键 `sort.Strings` 后输出；测试 `TestReplayMatchesBruteForce`。
2. **幂等确定性**：`api/api.go` 的 `Replay`/`History` 只持读锁、不写状态，`seq.Log.History` 返回副本；测试 `TestReplayIdempotentAndHistorySorted`。
3. **winner 稳定**：`lww/lww.go` `Apply` 中 `c.Ver > w.Ver || (c.Ver == w.Ver && c.Sn > w.Sn)`；测试 `TestWinnerInvariantAnyTime`（逐条喂、每步核对）。
4. **失败不留痕**：`api/api.go` `Feed` 先整体校验（`ErrEmptyKey`/`ErrNegativeVer`/`ErrHistoryLimit` 三哨兵）后整体应用，校验不过零写入；测试 `TestRejectedFeedLeavesNoTrace`。
