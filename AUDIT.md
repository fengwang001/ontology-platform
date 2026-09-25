# 审计：语义 → 代码位置 → 钉住测试

| 语义 | 保证位置 | 钉住测试 |
| --- | --- | --- |
| 1 往返/反向/行尾保留 | lines.Split/Join；udiff.Render 行尾与 marker；patch.ApplyParsed/Reverse | TestRoundTrip |
| 2 最短 = N+M−2LCS | edit.Diff Myers 主循环与 backtrack | edit.TestShortestAgainstDP |
| 3 确定性/删除优先 | edit.Diff 中 down>=right；backtrack 同规则 | edit.TestDeterministic、edit.TestDeleteFirstTie |
| 4 零计数头行号 | hunk.startCoord；udiff.coord | TestZeroHeaders |
| 5 hunk 合并阈值 2C | hunk.Build gap<=2C | TestMergeThreshold |
| 6 无换行 marker | hunk.item NoNL；udiff.Render/Parse marker | TestRoundTrip、TestOnlyEOLChange |
| 7 偏移应用最近/靠前 | patch.locate 按 dist、pos 稳定排序 | TestOffset |
| 8 原子拒绝+错误分类 | patch.ApplyParsed 逐 hunk，失败返回 nil | TestAtomicReject |
| 9 严格解析/行号 | udiff.Parse 计数与前缀校验、FormatError.Line | TestStrictParse |
| 10 多文档并发 | patch.Store 互斥+Log；Replay 串行重放 | TestConcurrentStore |

补充钉住：截断不 panic 且失败零改动 TestTruncateEveryByte；逐字符翻转 TestFlipEachLine；字节/hunk 上限 TestResourceLimits；四类错误互异 TestErrorClassesDistinct；仅末尾换行 TestOnlyEOLChange；合并阈值两侧 TestMergeThreshold。

## 复杂度计数器实测（4(N+M)(D+1) 上界；大/小 ≤150）

| 规模 | 实测 steps | 上界 4·2N·(D+1) | 结论 |
| --- | --- | --- | --- |
| N=1000，改 3 处 | 997 | 56000 | 通过 |
| N=100000，改 3 处 | 99997 | 5600000 | 大/小≈100.3 ≤ 150，通过 |

## 差异上限

edit.Diff Options.MaxDistance 超限立即返回 ErrTooDifferent，由 edit.TestMaxDistance 钉住。
