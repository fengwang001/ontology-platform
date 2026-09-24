# ontology-306：异步 IO 算子有序/无序输出缓冲

## 第三节推导（cap=4；"扣"=已完成但被扣住未输出）

| # | 操作 | 有序输出 | 有序占用 | 有序拒 | 无序输出 | 无序占用 | 无序拒 |
|---|---|---|---|---|---|---|---|
| 1 | In(e1) | — | 1 | — | — | 1 | — |
| 2 | In(e2) | — | 2 | — | — | 2 | — |
| 3 | W(10) | — | 2 | — | — | 2 | — |
| 4 | In(e3) | — | 3 | — | — | 3 | — |
| 5 | Complete(e2) | — | 3 | — | e2 | 2 | — |
| 6 | In(e4) | — | 4 | — | — | 3 | — |
| 7 | Complete(e4) | — | 4 | — | —(扣) | 3 | — |
| 8 | Complete(e3) | — | 4 | — | —(扣) | 3 | — |
| 9 | In(e5) | — | 4 | 容量满 | — | 4 | — |
| 10 | Complete(e1) | e1,e2,W10,e3,e4 | 0 | — | e1,W10,e4,e3 | 1 | — |
| 11 | Complete(e5) | — | 0 | 未知完成 | e5 | 0 | — |

(甲) 若水位线不当屏障：第7步错出 e4、第8步错出 e3（e3、e4 越过 W10），第10步只出 e1,W10，第11步出 e5；总输出错成 e2,e4,e3,e1,W10,e5（正确为 e2,e1,W10,e4,e3,e5）。
(乙) 若占用误算为"未完成数"：第9步时 e2/e3/e4 已完成不计，占用被算成 1（仅 e1），In(e5) 被错误接受（占用变 2）；第10步出 e1,e2,W10,e3,e4 后占用 1，第11步 Complete(e5) 出 e5。正确结果：第9步被拒（容量满，占用保持 4），e5 从未入队，第11步被拒（未知完成），e5 永不输出。
(丙) 第10步无序一次输出 e1,W10,e4,e3：e1 完成即出，W10 屏障解除，被扣元素按完成先后释放（e4 第7步完成、先于第8步的 e3）。若错按输入顺序释放，第10步输出错成 e1,W10,e3,e4。

## 不变量落实（代码位置 → 钉住它的测试）

1. 与朴素参照一致：`api.naive` 每步从头扫描重判，`runSteps` 逐步比对 → `TestMatchesNaive`、`SelfCheck`。
2. 有序保序：`aqueue.ordered.drain` 只从队头出队，输出必为输入前缀 → `TestMatchesNaive`、`TestConcurrentComplete`。
3. 水位线屏障：`aqueue.unordered` 分段扣留 + `cascade` 级联释放 → `TestConcurrentComplete`、`TestMatchesNaive`。
4. 失败不留痕：一切校验先于状态变更（`aentry.CheckID/CheckWatermark`、`base.admit/find`）→ `TestRejectNoTrace`。
