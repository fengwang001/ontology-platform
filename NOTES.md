# NOTES — 等宽分桶增量直方图

## 八步推导（W=10, maxValue=100，桶 0..9 + 溢出桶 OF）

| 步 | 操作 | 之后活跃桶（桶→计数，消失的桶不写） |
|---|---|---|
| 1 | Add(15) | 1→1 |
| 2 | Add(25) | 1→1, 2→1 |
| 3 | Add(10) | 1→2, 2→1 |
| 4 | Add(100) | 1→2, 2→1, OF→1 |
| 5 | Add(105) | 1→2, 2→1, OF→2 |
| 6 | Remove(25) | 1→2, OF→2（桶 2 归 0，立即消失） |
| 7 | Remove(10) | 1→1, OF→2 |
| 8 | Add(20) | 1→1, 2→1, OF→2 |

- (甲) 下边界错 `(v-1)/W`：第 3 步 `Add(10)` → (10-1)/10=0，错落**桶 0**（正确桶 1）。上边界错（无溢出判定）：第 4 步 `Add(100)` → 100/10=10，错落**普通桶 10**（正确溢出桶 OF）。
- (乙) 第 6 步后桶 2 残留为 `2→0`：`Buckets()` 多出 `{2:0}`，`Count(2)` 返回 `(0, true)`；正确应为 `(0, false)`——桶已不存在，「零值」与「不存在」被混淆。
- (丙) `Add(100)` 进溢出桶（OF=1）；`Remove(100)` 按 `v/W` 算到桶 10，桶 10 不存在 → 拒绝；溢出桶计数永久残留 1，与批量重算（应为空直方图）不再一致，该值永远无法撤回。

## 四条不变量落点

1. 与批量重算一致：`hagg.Add/Remove` 只对目标桶 ±1，无旁路状态（hagg/hagg.go）；测试 `TestBatchEquivalence`。
2. 桶消失：`hagg.Remove` 中 count 归 0 即 `delete`（hagg/hagg.go）；测试 `TestBucketDisappears`。
3. 溢出桶正确：`hst.Bucket` 对 `v>=maxValue` 返回溢出桶号 `maxValue/W`，Add/Remove 共用同一归属函数（hst/hst.go）；测试 `TestOverflow`。
4. 失败不留痕：`hagg.Add/Remove` 先全部校验再改状态，四类哨兵错误互不相同（hagg/hagg.go、api/api.go）；测试 `TestFailures`。
