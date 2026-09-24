# NOTES

## 十操作推导表（写时复制正确实现）

| # | 操作 | cur id | 当前版本内容 | 本行读返回 |
|---|---|---|---|---|
| 1 | Update(A,1) | 1 | {A:1} | - |
| 2 | Update(B,2) | 2 | {A:1,B:2} | - |
| 3 | S=Snapshot() | 2 | {A:1,B:2} | S 钉住 id2 |
| 4 | Read(A) | 2 | {A:1,B:2} | "1" |
| 5 | Update(A,9) | 3 | {A:9,B:2} | - |
| 6 | Read(A) | 3 | {A:9,B:2} | "9" |
| 7 | S.Read(A) | 3 | {A:9,B:2} | "1"（id2 永不变） |
| 8 | ReadKeys([A,B]) | 3 | {A:9,B:2} | {A:9,B:2} |
| 9 | Update(B,5) | 4 | {A:9,B:5} | - |
| 10 | ReadKeys([A,B]) | 4 | {A:9,B:5} | {A:9,B:5} |

- **(甲)** 第 7 步返回 "1"；若不 COW、原地改共享 map，id2 的 map 被第 5 步污染，S.Read(A) 错成 "9"。
- **(乙)** 第 8 步返回 {A:9,B:2}；若逐 key 两次独立 Load，A 取自 id3、B 取自 id5({A:3,B:5})，撕裂成 {A:9,B:5}，不属于任何真实版本。
- **(丙)** 无锁两写者都从 {A:1} 克隆：甲发 {A:1,B:2}，乙覆盖发 {A:1,C:3}，**丢失 B**；加锁正确结果 {A:1,B:2,C:3}。

## 四条不变量：保证位置 + 钉住的测试

1. 与批量参照一致：`snapshot.Store.Update` 克隆整图、构造新 `ver.Version` 后一次 atomic Store 发布；测试 `TestBatchReferenceConsistency`。
2. 快照不可变：每次 Update 必走 `ver.Clone`，`snapshot.Handle` 持有固定 `*ver.Version` 指针；测试 `TestSnapshotHandleImmutable`。
3. 读不阻塞写、读永远一致：`Read/ReadKeys/Snapshot` 均无锁且只 Load 一次，ReadKeys 多 key 取自同一版本；测试 `TestConcurrentReadersSeeWholeVersion`、`TestReadAccessCounterDoesNotGrowWithMapSize`。
4. 失败不留痕：`api.API.Update` 在取锁/克隆/Store 之前完成全部校验，仅返回哨兵错误；测试 `TestRejectedUpdateLeavesStateUnchanged`、`TestSelfCheck`。
