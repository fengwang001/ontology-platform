# NOTES — 最小最大堆双端优先队列

## 第三节：八步推导（多重集 / Min / Max / Delete 返回值）

| # | 操作 | 之后的多重集 | Min | Max | Delete 返回 |
|---|------|--------------|-----|-----|-------------|
| 1 | Push(5) | {5} | 5 | 5 | — |
| 2 | Push(8) | {5,8} | 5 | 8 | — |
| 3 | Push(3) | {3,5,8} | 3 | 8 | — |
| 4 | Push(8) | {3,5,8,8} | 3 | 8 | — |
| 5 | DeleteMin() | {5,8,8} | 5 | 8 | 3 |
| 6 | Push(1) | {1,5,8,8} | 1 | 8 | — |
| 7 | DeleteMax() | {1,5,8} | 1 | 8 | 8 |
| 8 | DeleteMax() | {1,5} | 1 | 5 | 8 |

- (甲) 双堆不同步：Push(2),Push(5),Push(7) 后 DeleteMin 只删最小堆的 2，最大堆仍残留 {2,5,7}。
  DeleteMax→7、DeleteMax→5 之后队列实际已空，但再 DeleteMax 会**错误返回 2**（最大堆里没同步删掉的 2）；本应 `ok=false`（队列已空）。
- (乙) Max 只在 Push 时缓存：第 7 步 DeleteMax 后缓存仍是 8。因多重集里还有一个 8，数值上碰巧等于真实 Max=8，
  但缓存指向的已被删除；错误在第 8 步后暴露：Max() **错返 8，本应返回 5**。
- (丙) {1,5} 再 DeleteMin→1、DeleteMax→5 后为空：Min/Max/DeleteMin/DeleteMax 全部返回 `ok=false`（零值），
  Len 保持 0 不变；只剩一个元素时 Min==Max==该元素。

## 第二节：四条不变量及保证位置 / 钉住测试

1. 与朴素参照一致：dpq 直接透传 mmheap 四操作；mmheap 根即最小、根的较大子即最大 → `TestNaiveConsistency`（api_test）。
2. 结构正确：mmheap.Push 的 `up`、Delete 的 `down` 沿交替层维护不变量，`Check` 全树遍历核验 → `TestStructure`（mmheap_test）。
3. 守恒：`Len()==len(h.a)`；Push 必 append 增一、removeAt 必截尾减一 → `TestConservation`（api_test）。
4. 失败不留痕：空时四操作直接返回 `ok=false`，不触碰底层数组 → `TestEmptyBoundaries`（api_test）。
