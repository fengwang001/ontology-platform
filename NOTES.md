# Slab 分配器推导（raw=10, align=8, slabSize=60）

sz=alignUp(10,8)=16；perSlab=60/16=3（整除）；尾部浪费=60-3*16=12。
八步表（F=full P=partial E=empty；分配顺序=字典序最小 (slab,槽位) 对）：
1. Alloc -> 0       slab0: P
2. Alloc -> 16      slab0: P
3. Alloc -> 32      slab0: F
4. Alloc -> 60      slab0: F, slab1: P
5. Free(16)        slab0: F->P; slab1: P
6. Free(60)        slab1: P->E; slab0: P
7. Alloc -> 16      slab0: P->F; slab1: E
8. Alloc -> 60      slab1: E->P; slab0: F

甲：按原始大小 perSlab=60/10=6；槽位在 0,10,20,30，第 4 对象 off=30 区间[30,40)；30 不是 align=8 的倍数（更非真实 sz=16 的倍数），与合法槽 [32,48) 重叠，非法。
乙：raw+(align-raw%align) 不判零余 => sz=8+(8-0)=16；正确 sz=8（已是 align 整数倍时不变）。
丙：ceil(60/16)=4 个/slab；第 4 槽 off=48 区间 [48,64)，跨 slab 边界 60，与下一 slab 槽 0 [60,76) 重叠 [60,64)。

不变量 -> 代码保证位置 -> 钉住测试：
1. 守恒与唯一性：cache.go Alloc 写 live map（偏移作 key 天然去重、唯一）+ check.go verify 按各 slab used 求和对账 -> TestConservation
2. 与朴素参照一致：cache.go Alloc 经 partial/empty 最小堆取编号最小 slab、其内 minFree 取最小空槽 -> TestNaiveEquivalence
3. 打包合法：slab/geometry.go 的 Offset/Split/Contains 换算与边界 + check.go verify 逐个 Contains（尾浪费永不可达）-> TestPacking
4. 失败不留痕：cache.go NewCache 全部校验先于构造状态、Free 先查 live 命中才改 -> TestErrorsAtomic
