# NOTES — 投影下推与列裁剪

推导基准：cols=[a,b,c,d,e]；输出列依次 x←[a]、sum←[b,c]、y←[d]；三行按 a,b,c,d,e 给值。

| 步骤 | 保留源列（refs 并集） | 被裁剪源列 |
|---|---|---|
| 定义 x←[a] 后 | {a} | {b,c,d,e} |
| 再定义 sum←[b,c] 后 | {a,b,c} | {d,e} |
| 再定义 y←[d] 后 | {a,b,c,d} | {e} |
| 变更行 | 投影 (x,sum,y) | — |
| (1,2,3,4,5) | (1,5,4) | — |
| (6,7,8,9,10) | (6,15,9) | — |
| (11,12,13,14,15) | (11,25,14) | — |

- (甲) 按输出名读源列：源列无 x → 未知列/零值兜底，第一行 x 错成 **0**（正确 1）。
- (乙) 误裁 b、c：读不到按 0，第一行 sum 错成 **0**（正确 2+3=5）。
- (丙) 首算后缓存：第二行 sum 错成 **5**（正确 7+8=15）；每行相互独立，必须用本行值现算。

## 四条不变量落点

1. 与全量重算一致：`prune.Project` 只装保留列后交 `col.Eval` 按 refs 对本行求和；测试 `TestViewMatchesFullRecompute`。
2. 列裁剪精确：`prune.Set` 求 refs 并集（schema 序），`Project` 只遍历 retained，读一个计一次 `lastRead`；测试 `TestPrunedReadsIndependentOfWidth`。
3. 输出稳定：严格按 defs 定义顺序产出，`Names()` 取各列 Out；测试 `TestOutputOrderAndNames`。
4. 失败不留痕：`api.SetProjection`/`Apply` 全部校验通过后才在锁内替换定义或追加行；测试 `TestRejectedOpsLeaveNoTrace`。

并发只读一致由 `TestConcurrentViewReaders` 钉住（`go test -race`）；四类哨兵互异由 `TestSentinelErrorsDistinct` 钉住。
