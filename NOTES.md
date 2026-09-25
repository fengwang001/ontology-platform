# NOTES

## 七步推导（排序键 = (priority 升序, 注册序 # 升序)，#n 为注册序号）

| 步 | 操作 | 操作后队列按 (priority, #) 升序（= 连续 Pop 顺序） |
|---|---|---|
| 1 | Push("a",10) | a(10,#1) |
| 2 | Push("b",5) | b(5,#2) a(10,#1) |
| 3 | Push("c",5) | b(5,#2) c(5,#3) a(10,#1) |
| 4 | Push("d",10) | b(5,#2) c(5,#3) a(10,#1) d(10,#4) |
| 5 | UpdatePriority("b",20) | c(5,#3) a(10,#1) d(10,#4) b(20,#2) |
| 6 | Delete("c") | a(10,#1) d(10,#4) b(20,#2) |
| 7 | Push("c",8) | c(8,#5) a(10,#1) d(10,#4) b(20,#2) |

- (甲) 第 4 步后堆根是 b。若 increase-key 只上浮不下沉，b(20) 滞留根位，堆最小值错成 b(20)；下一次 Pop 返回 (b,20)，本应返回 (c,5)。
- (乙) 正确顺序 b 先于 c（#2 < #3）。只比 priority 时 b、c 次序由堆形状偶然决定，可能给出 c 先于 b 的错误顺序。
- (丙) Delete 残留 "c" 的映射 → 第 7 步 Push("c",8) 被误判为重复 id 而拒绝，c 彻底丢失；连续 Pop 得 [a,d,b]，漏掉 c，本应为 [c,a,d,b]。

## 不变量与保证位置 / 钉住测试

1. 与朴素参照一致：比较键 (pri,seq) 集中于 `ipq.less`，弹出即有序 ⇒ `api_test.TestNaiveReference`。
2. 堆性质：`ipq.siftUp/siftDown` 只沿堆路径交换并同步 pos ⇒ `ipq_test.TestHeapProperty`。
3. 删除彻底：`ipq.Delete` 删 pos 映射，`pq` 不另存活跃 id ⇒ `api_test.TestDeleteRepush`。
4. 失败不留痕：`pq` 先校验（空 id/重复/不存在）后改状态，seq 校验通过才递增 ⇒ `api_test.TestErrors`。

复杂度：上浮/下沉只走堆路径，检查数 ≤ O(log m)，由非导出计数器 `ipq.Heap.checked` 记录 ⇒ `ipq_test.TestSiftComplexity`（同包测试直读字段，不经导出接口）。
