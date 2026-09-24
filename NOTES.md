# NOTES

## 第三节：七步分步表（消费者每步用上一步 next 续读）
| 步 | HW | 未决事务(生产者@首位点) | LSO | Fetch 区间 | 输出 |
|---|---|---|---|---|---|
| 1 | 2 | pid1@0, pid2@1 | 0 | [0,0) | 无 |
| 2 | 4 | pid2@1 | 1 | [0,1) | a |
| 3 | 6 | pid2@1, pid3@4 | 1 | [1,1) | 无 |
| 4 | 7 | pid3@4 | 4 | [1,4) | c（b 属已中止；位点 3 是标记） |
| 5 | 9 | pid3@4, pid1@8 | 4 | [4,4) | 无 |
| 6 | 10 | pid1@8 | 8 | [4,8) | d, f（e 中止；位点 6 是标记） |
| 7 | 11 | 无 | 11 | [8,11) | 无（g 中止；位点 9、10 是标记） |
正确拼接序列：a, c, d, f。
(甲) 用 HW 代替 LSO（只过滤此刻已知中止、仍跳标记）：第 1 步 [0,2) 输出 a, b；第 2 步 [2,4) 输出 c。其中 **b** 随后被位点 6 的 Abort(2) 证明属于中止事务（脏读）。
(乙) 不过滤中止（LSO 为界、跳标记）：拼接 a, b, c, d, e, f, g；正确为 a, c, d, f。若连控制标记也当记录输出，再多 **4** 条（位点 3、6、9、10）。
(丙) 按生产者而非事务判定中止：HW=11 后首次 Fetch(0) 时 pid1 的末次标记是 Abort，a、c 被误杀，只读到 d, f；正确应读 a, c, d, f。差异来自规则：结局按**事务**判定，控制标记只结束该生产者当前这一个事务，不影响其此前已提交事务。

## 四条不变量：保证位置与钉住的测试
1. 与批量参照一致：rc.Read 以 LSO 为上界、返回 next=LSO，且 LSO 单调（txlog AdvanceHW）；钉住测试 TestStreamingMatchesBatch（含随机交错循环）。
2. LSO 合法且单调：txlog.(*Log).AdvanceHW 的未决最小堆循环——LSO 要么为 HW 要么为堆顶事务首位点，堆顶只出不进故只增；钉住测试 TestLSOMonotonic。
3. 不暴露未决与中止：rc.Read 跳过 Kind!=Data，且仅放行 Committed 且 End<HW 的事务；钉住测试 TestFetchVisibility。
4. 失败不留痕：txlog AppendData/appendMarker/AdvanceHW 与 rc.Read 全部先校验、后改状态；钉住测试 TestRejectedOpsNoTrace。
