# NOTES — per-key debounce refresher（W=3 八步推导）

| 步 | k 批次(条/值/时刻) | m 批次 | 刷新输出 | 视图 |
|---|---|---|---|---|
| 1 `R(k,0,a)` | 1/a/3 | 无 | 无 | {} |
| 2 `R(k,2,b)` | 2/b/5 | 无 | 无 | {} |
| 3 `R(m,2,x)` | 2/b/5 | 1/x/5 | 无 | {} |
| 4 `Tick(4)` | 2/b/5 | 1/x/5 | 无 | {} |
| 5 `R(k,5,c)` | 3/c/8 | 1/x/5 | 无 | {} |
| 6 `Tick(5)` | 3/c/8 | 无 | (m,1,x) | {m:x} |
| 7 `R(m,5,y)` | 3/c/8 | 1/y/8 | 无 | {m:x} |
| 8 `Stop(8)` | 无 | 无 | (k,3,c),(m,1,y) | {k:c,m:y} |

**甲**：第5步并入旧批（k 变 3/c/8）。Record 只看「待刷新批是否存在」；t=5 虽恰等于旧 deadline 5，但 Tick 尚未执行，该批仍 pending，相等边界属于 Tick 的 `deadline<=now` 谓词，与 Record 无关。若 Tick 误写为严格 `<`：第6步视图应为 {m:x}，错成 {}（m 该刷未刷）；第7步 y 并入本应已走的 x 批 → m{2,y,8}；Stop 后 m 由正确的「2 批各 1 条」错成「1 批 2 条」（最终值恰好仍是 y，但批结构错）。
**乙**：合并不顺延时，第2步后 k deadline 固定为首条 0+3=3；第4步 Tick(4) 中 k 应不可见，错成提前可见且值为 b（合并窗被破坏，b 本应到 t=5 才可刷）。
**丙**：把 m 两条变更（x@2、y@5，间隔恰 =W=3）变为相邻、中间无 Tick：y 到达时 t=5 恰为该批 deadline，批仍 pending → 并入成 m{2,y,8}；Tick(5) 不刷 m，Stop 只出 1 批 (m,2,y)。最终视图与主序相同 {k:c,m:y}，但 m 批结构由「2 批各 1 条、中途曾显示 x」变为「1 批 2 条、中途从不出现」。差异来自「合并/顺延只认待刷新批存在性」叠加「视图仅在 Tick/Stop 更新」：记录集合相同，Tick 时机不同则批结构不同。故不变量 1 只能钉 Stop 后最终值——防抖期视图刻意滞后（主序第 7 步已接受 y，视图仍是 x），任意时刻本就不应反映最新变更。

## 不变量保证位置与测试锚点

1. 最终值等价：`thr.Stop` 排空全部 pending 批、`deb.Merge` 以后到值覆盖（thr.go / deb.go）；由 `TestFinalEquivalence`（随机序列对朴素分组取 max-t、并列后者胜）钉住。
2. 节流：`deb.Batch` 仅在 Merge 时 count++，`thr` 每弹出一批 `fired++` 且写入值必来自某条 Record；由 `TestThrottleCounts`、`TestEightSteps` 钉住。
3. 时钟单调：`thr.Record/Tick/Stop` 一律先校验时间再改状态，`last` 只在成功路径末尾推进，拒绝即原样返回；由 `TestMonotonicNoTrace` 钉住。
4. 失败不留痕：`thr.New` 拒非正参数，`thr.Record` 先判空 key、时间回退、超限，全部通过后才落盘修改；由 `TestRejectNoTrace`（四类哨兵 errors.Is + 拒绝前后 View/Fired 快照）钉住。
