# ontology-449 推导与不变量

hotCap=2, maxCold=100，时钟从 1 起，每次 Put/Get 被触键 atime=时钟 后自增。

| 步 | 操作 | 热层 MRU→LRU | 冷层字典序 | 换出 | 返回 |
|---|---|---|---|---|---|
| 1 | Put(A,1) | [A] | [] | 无 | 无 |
| 2 | Put(B,2) | [B,A] | [] | 无 | 无 |
| 3 | Get(A) | [A,B] | [] | 无 | 1 |
| 4 | Put(C,3) | [C,A] | [B] | B | 无 |
| 5 | Get(B) | [B,C] | [A] | A | 2 |
| 6 | Put(D,4) | [D,B] | [A,C] | C | 无 |
| 7 | Get(A) | [A,D] | [B,C] | B | 1 |
| 8 | Put(E,5) | [E,A] | [B,C,D] | D | 无 |

(甲) 第4步正确换出 B（atime：B=2 最早；第3步 Get(A) 已把 A 刷成 3）。若 Get 不刷新、退化为 FIFO，则 A 写入最早被错换，冷集错成 {A}（应为 {B}）。
(乙) 条件误写成 >=：第2步热层达 2 即换出 LRU=A，错成热={B}、冷={A}（正确：热={A,B}、冷={}）。
(丙) 换出丢值：第5步 Get(B) 错返 0（零值），正确应保留并返回 2。

## 四条不变量的保证位置与钉住测试

1. 与朴素参照一致：值只存于 store.go 的 hotV/coldV 两张值表，Get 永不写值、换出换入只搬值——`(*Store).Put/Get/Value`；测试 TestNaiveReference。
2. 分层一致（并集=全集、交集=∅、不超容）：store.go `(*Store).Put` 的预检+换出块与 `(*Store).Get` 的换入块，每次仅在两层间搬一个键；测试 TestTiersDisjointAndCapacity。
3. LRU 顺序正确：lru.go `(*LRU).Touch` 赋 atime=当前时钟并 MoveToFront，HotKeys 按链表 MRU→LRU；测试 TestHotKeysFollowAtime（八步序列另由 TestEightStepTrace 钉住）。
4. 失败不留痕：store.go `New` 容量校验、`Put`/`Get` 在任何写入前返回 ErrEmptyKey/ErrNotFound，ErrColdFull 由写入前的换出后冷层计数预检拒绝；测试 TestRejectedOperationsAreAtomic。
