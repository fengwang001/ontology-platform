# NOTES — 热冷两层状态存储

## 第三节推导：hotCap=2, maxCold=100，八步分步表

| 步 | 操作 | 热层(MRU→LRU) | 冷层(字典序) | 本步换出 | Get 返回 |
|---|---|---|---|---|---|
| 1 | Put(A,1) | A | （空） | 无 | 无 |
| 2 | Put(B,2) | B A | （空） | 无（2≤2 不换出） | 无 |
| 3 | Get(A) | A B | （空） | 无 | 1 |
| 4 | Put(C,3) | C A | B | B（热层 3>2，LRU=B@2） | 无 |
| 5 | Get(B) | B C | A | A（B 换入为 MRU，LRU=A@3） | 2 |
| 6 | Put(D,4) | D B | A C | C（LRU=C@4） | 无 |
| 7 | Get(A) | A D | B C | B（A 换入为 MRU，LRU=B@5） | 1 |
| 8 | Put(E,5) | E A | B C D | D（LRU=D@6） | 无 |

- (甲) 第 4 步正确换出 **B**（Get(A) 已把 A 刷成 MRU）。若 Get 不刷新访问时间（退化为写入序 FIFO），最旧写入是 A，会错换出 **A**；冷集错成 **{A}**（应为 {B}）。
- (乙) 换出条件错写成 `>= hotCap`：第 2 步 Put(B,2) 后热层键数 2>=2，错换出 **A**；第 2 步后热集错成 **{B}**、冷集错成 **{A}**（应为热 {B,A}、冷 {}）。
- (丙) 换出丢值时，第 5 步 Get(B) 换入后读到零值，错返回 **0**；正确应返回 **2**（值在冷层完整保留）。

## 四条不变量：保证位置与钉住测试

1. 与朴素参照一致：值只经 `store.Store.Put/Get` 写入 `val` 映射、换出不删值（store.go `Put/Get/evictIfNeeded`）；测试 `TestNaiveConsistency`。
2. 分层一致：键只在 `hot`(lru) 或 `cold` 集合之一，换入换出成对迁移、容量由 `needEvict` 在变更前校验（store.go）；测试 `TestTierInvariant`。
3. LRU 顺序：`lru` 双向链表头=MRU，`Touch/Add` 提到头、`LruKey` 取尾（lru/lru.go）；测试 `TestLRUOrder`。
4. 失败不留痕：`Put/Get` 先做全部校验（空键/不存在/`needEvict` 冷层满）再动任何状态（store.go 前置校验段）；测试 `TestFailureNoTrace`。
