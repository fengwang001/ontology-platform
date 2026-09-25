# Quickselect Notes

## 分区后的递归方向
partition 后数组为 `[<pivot] [pivot] [>pivot]`，pivot 已位于最终位置 `p`。
由于左侧恰有 `p` 个元素，全局排序中下标 `p` 的值就是 pivot：
- `p == k`：已找到排序后下标 `k` 的元素，返回 pivot。
- `p > k`：目标下标仍在左侧，只对 `[lo,p-1]` 递归。
- `p < k`：目标下标在右侧，只对 `[p+1,hi]` 递归。

不能比较 pivot 的“值”和 `k`：`k` 是目标下标，pivot 是元素值，二者量纲不同。
正确判据只依赖 pivot 的最终位置 `p` 与下标 `k` 的大小关系。

## 语义索引
（测试完成后补充）
