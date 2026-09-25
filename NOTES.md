# 对齐分配器 NOTES

## 第三节：八步推导（headerSize=8，next 从 0 开始）

| # | 操作 | reserve | base | 返回 ptr / Free 读出的 base | 操作后 next |
|---|------|---------|------|------------------------------|-------------|
| 1 | Alloc(10,16) | 10+15+8=33 | 0 | ptr=alignUp(0+8,16)=16 | 33 |
| 2 | Alloc(4,8) | 4+7+8=19 | 33 | ptr=alignUp(33+8,8)=alignUp(41,8)=48 | 52 |
| 3 | Alloc(8,8) | 8+7+8=23 | 52 | ptr=alignUp(52+8,8)=alignUp(60,8)=64 | 75 |
| 4 | Free(16) | - | - | 读 [8,16) → base=0 | 75 |
| 5 | Free(48) | - | - | 读 [40,48) → base=33 | 75 |
| 6 | Alloc(1,8) | 1+7+8=16 | 75 | ptr=alignUp(75+8,8)=alignUp(83,8)=88 | 91 |
| 7 | Alloc(16,16) | 16+15+8=39 | 91 | ptr=alignUp(91+8,16)=alignUp(99,16)=112 | 130 |
| 8 | Free(64) | - | - | 读 [56,64) → base=52 | 130 |

- **(甲)** 正确 ptr = alignUp(41,8) = **48**。若不做对齐直接 ptr = base+headerSize = 33+8 = **41**，41 = 5×8+1，**不是** 8 的整数倍，不变量 1 当场被破坏。
- **(乙)** 头部若写在 [base, base+8) 即 [0,8)，则 Free(16) 读的 [8,16) 是载荷区、从未写过头部；本例因字节空间零初始化**碰巧**读到 0，看似正确，但第 5 步 Free(48) 读 [40,48) 得 0 ≠ 真实 base 33。后果：base 记账全错，释放回溯失效，任何依赖 base 的统计/回收都作用在错误区域。
- **(丙)** 不校验 2 的幂时 Alloc(4,6) 会被接受：位运算 alignUp(8,6) = (8+6-1) &^ (6-1) = 13 &^ 5 = **8**，而 8 % 6 = 2 ≠ 0，返回的 ptr 不是 6 的倍数，不变量 1 被破坏。正确行为：拒绝并报 **ErrInvalidAlign**。

## 第二节：四条不变量 → 代码位置 → 钉住它的测试

1. **对齐合法**：`alloc.Alloc` 中 `ptr = align.Up(base+HeaderSize, align)`（alloc/alloc.go）→ `TestTrace8`、`TestNaiveModel`、`TestConcurrentAlloc`
2. **与朴素参照一致**：bump `next` + `live`/`hdr` 两张表（alloc/alloc.go）→ `TestNaiveModel`（多种子随机操作序列逐条对比）
3. **释放回溯正确**：`Free` 从 `hdr[ptr-HeaderSize]` 读 base（alloc/alloc.go）→ `TestTrace8`、`TestNaiveModel`
4. **失败不留痕**：`align.Check` 先于任何状态修改执行（alloc/alloc.go）→ `TestErrorsDistinct`（拒绝前后 next/live/hdr 快照逐项相等）
