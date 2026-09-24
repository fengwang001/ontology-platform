# 撤回日志按水位回收器 — 推导与不变量

## 八步分步表（G=回收上界，含等号回收 Seq<=G）

| 步 | 操作 | 活跃快照(水位) | G | 已回收 Seq |
|---|---|---|---|---|
| 1 | Append×4 (Seq1..4) | — | 0 | {} |
| 2 | Open S1 (W=4) | S1(4) | 0 | {} |
| 3 | Append (Seq5) | S1(4) | 0 | {} |
| 4 | Open S2 (W=5) | S1(4) S2(5) | 0 | {} |
| 5 | GC → G=min(4,5)=4 | S1(4) S2(5) | 4 | {1,2,3,4} |
| 6 | Append (Seq6) | S1(4) S2(5) | 4 | {1,2,3,4} |
| 7 | Close S1 | S2(5) | 4 | {1,2,3,4} |
| 8 | GC → G=5 | S2(5) | 5 | {1,2,3,4,5} |

- (甲) 含等号：第5步正确回收 {1,2,3,4}，Seq=4 被收。若错写成 `Seq<G`：第5步 Seq=4 不被回收（只收 {1,2,3}）；第8步 Close S1 后 G=5，此时 Seq=4<5 才被补收，期间它滞留日志。
- (乙) 若 GC 无视快照按「Seq<=当前Seq」全收：第5步回收 {1,2,3,4,5}；S1(W=4) Replay(4) 报「已回收」。违反不变量1（快照不丢数据）与不变量2（G 应等于最小活跃水位 4，而非当前 Seq 5）。
- (丙) 正确实现第8步回收 Seq=5（累计 {1..5}）。若 Close 不重估最小水位（缓存旧值 4）：第8步 G 仍是 4，Seq=5 被漏回收、永久滞留。违反不变量2（回收上界≠最小活跃水位 5）。

## 四条不变量：保证位置与钉住它的测试

1. 快照不丢数据：`gcer.(*Log).GC` 只收 `Seq<=G`，`G=min(活跃水位)`；`Replay` 对 `G<seq<=W` 必命中。测试 `TestInvariantSnapshotNoLoss`。
2. 回收上界=最小活跃水位：`gcer.(*Log).minWatermark`（小顶堆取堆顶，无活跃快照时取当前 Seq）。测试 `TestInvariantWatermarkEqualsMinActive`。
3. 水位单调：`gcer.(*Log).GC` 中 `if g>l.wm` 才前进。测试 `TestInvariantMonotoneWatermark`。
4. 失败不留痕：`Open`/`Close`/`Replay` 全部先校验后变更，拒绝路径零写。测试 `TestInvariantFailureNoTrace`、`TestFaultInjection`。

复杂度：`gcer` 内非导出计数器 `checked` 记录确定最小水位时检查的快照数，堆顶 O(1) 取最小，测试 `TestMinWatermarkCheckCount`（包内白盒，不经导出接口）。并发：`TestConcurrentOpenCloseAppend`；自检：`api.(*Engine).SelfCheck`，测试 `TestSelfCheck`。
