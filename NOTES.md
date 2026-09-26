# 两级完美哈希（FKS）— 推导与不变量

键集 {5,11,13,17,19,24}，m=6，p=29；h1(x)=x mod 6，hj(x)=(a·x+b mod 29) mod n_j²。

八行分步表：
1. h1(5)=5 → 桶5
2. h1(11)=5 → 桶5
3. h1(13)=1 → 桶1
4. h1(17)=5 → 桶5
5. h1(19)=1 → 桶1
6. h1(24)=0 → 桶0（n=1，直接槽，不建二级表）
7. 桶1={13,19}，m_j=4，(a,b)=(1,0) 即无碰撞：13→槽1，19→槽3
8. 桶5={5,11,17}，m_j=9，(a,b)=(1,0) 即无碰撞：5→槽5，11→槽2，17→槽8

(甲) m_j 错用 n_j=3：5%3=11%3=17%3=2，三键全部碰撞在槽 2。
(乙) Lookup(7)：桶=7%6=1，槽=(7 mod 29) mod 4=3，槽3 存的是 19≠7，应返回 (false, ErrNotFound)；若不比对槽内键，会错误返回 19。
(丙) 漏掉最后的 mod m_j：槽位=x mod 29，13→槽13、19→槽19，均 ≥ m_j=4，越出只有 4 个槽的二级表（下标越界）。

四条不变量（保证位置 / 钉住它的测试）：
1. 无碰撞查找：second/second.go 的 search 按 (a,b) 顺序试到桶内两两不同，Lookup 比对槽内键；TestNoCollision。
2. 与朴素参照一致：api/api.go 的 Lookup 命中当且仅当槽内键==x；TestNaiveReference。
3. 空间 Σn_j²：second/second.go 的 Build 按 n_j² 分配、直接槽不建表，Space() 双向对账；TestSpace。
4. 失败不留痕：api/api.go 的 Build 先全部校验再构建、Lookup 纯只读；TestRejectionKeepsState。
