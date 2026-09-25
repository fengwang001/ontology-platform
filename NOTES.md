# NOTES

## 推导：rawSize=10, align=8, slabSize=60

sz = alignUp(10,8) = 16；perSlab = 60/16 = 3；尾部浪费 = 60-3*16 = 12。
slab0 槽位 [0,16,32]，slab1 槽位 [60,76,92]。

| # | 操作 | 结果 | slab0 | slab1 |
|---|------|------|-------|-------|
| 1 | Alloc | off=0 | partial | - |
| 2 | Alloc | off=16 | partial | - |
| 3 | Alloc | off=32 | full | - |
| 4 | Alloc | off=60（新建 slab1 槽0） | full | partial |
| 5 | Free(16) | slab0 full→partial | partial | partial |
| 6 | Free(60) | slab1 partial→empty | partial | empty |
| 7 | Alloc | off=16（slab0 最小空闲槽1） | full | empty |
| 8 | Alloc | off=60（无 partial，取 empty slab1 槽0） | full | partial |

(甲) 按原始大小打包：perSlab = 60/10 = 6；第 4 个对象落在偏移 30。30 不是真实 sz=16 的整数倍，且 30%8=6≠0，违反 align=8，非法。
(乙) sz = 8 + (8 - 8%8) = 16；正确应为 8（raw 已是 align 整数倍时不变）。
(丙) ceil(60/16) = 4；最后一个对象占 [48,64)，越界并与 slab1 槽位0 [60,76) 在 [60,64) 重叠。

## 不变量 → 代码位置 → 测试

1. 守恒与唯一：cache.Alloc/Free 用 allocated map 记账、每 slab used 计数；TestConservation。
2. 与朴素参照一致：cache 按"最小空闲槽/partial 优先"取槽；TestNaiveReference（随机序列对拍 + 全 Free 后无 full）。
3. 打包合法：slab.AlignUp 与 perSlab=floor 整除，槽位 i*sz 永不越界；TestPacking。
4. 失败不留痕：cache 先校验后变更，哨兵错误；TestFailureAtomic。
