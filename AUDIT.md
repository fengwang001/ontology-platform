# AUDIT

## 第二节十条语义逐条核对

| #   | 语义 | 代码保证位置 | 钉住它的测试 |
| --- | --- | --- | --- |
| 1 | 往返逐字节相等、行尾保留 | `lines.Split/Join` 保留行尾（lines/lines.go）；`udiff.Render` 输出 `\ No newline` 标记、`Parse` 还原（udiff/udiff.go）；`patch.run` 精确匹配替换（patch/patch.go） | `TestRoundTrip`、`TestNewlineOnly` |
| 2 | 编辑脚本最短 | `edit.Diff` 的 Myers O(ND) 主循环（edit/edit.go） | `TestShortest`（O(NM) DP 对照） |
| 3 | 确定性 | `edit.Diff` 平局固定选删除（`<` 而非 `<=`），回溯同条件；无 map 迭代、无随机 | `TestDeterministic` |
| 4 | 零计数 hunk 头行号 | `hunk.build` 中 `OldStart--/NewStart--`（hunk/hunk.go）；`patch.sides` 中 `pos = start`（patch/patch.go） | `TestZeroCountHeaders` |
| 5 | hunk 合并阈值 2C | `hunk.Group` 中 `k-j > 2*ctx` 拆分（hunk/hunk.go） | `TestMergeThreshold`（g=2C 与 2C+1） |
| 6 | 无换行标记 | `udiff.Render` 对无 `\n` 结尾的行追加标记；`Parse` 遇 `\` 行剥掉上一行 `\n`（udiff/udiff.go） | `TestNewlineOnly`、`TestRoundTrip` |
| 7 | 偏移查找最近、同距靠前 | `patch.locate` 升序扫描、仅在距离严格更小时更新（patch/patch.go） | `TestOffset` |
| 8 | 原子拒绝 | `patch.run` 在内存行切片上操作、失败返回 nil；`Store.Apply` 成功才替换（patch/patch.go） | `TestAtomic` |
| 9 | 解析严格性 | `udiff.Parse` 的计数循环：多行/少行/非法前缀均报带行号错误（udiff/udiff.go） | `TestParseStrict` |
| 10 | 多文档并发一致 | `Store` 互斥锁内快照-应用-提交并记日志（patch/patch.go） | `TestStoreConcurrent`（-race） |

## 第四节复杂度计数实测

| 规模 | 实测步数 | 上界 4(N+M)(D+1) | 结论 |
| --- | --- | --- | --- |
| N=M≈1000，D=4 | 1011 | 40000 | 通过 |
| N=M≈100000，D=4 | 100011 | 4000000 | 通过 |

比值 100011/1011 ≈ 99 ≤ 150，近似线性（`TestCounter`）。上限触发时计数同样满足上界（`TestMaxDist`）。

## 第五节故障注入

逐字节截断不 panic、可解析则可应用（`TestTruncation`）；逐行翻转字符要么解析拒绝要么应用拒绝且原文不变（`TestFlip`）；字节数/hunk 数上限（`TestLimits`）；四类错误可区分（`TestErrorKinds`）。
