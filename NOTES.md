# NOTES — 时间旅行 as-of 查询

## 一、八步推导（key = K，链按 ts 升序）

| 步 | 操作 | 操作后 K 的版本链 | as-of 结果 |
|---|---|---|---|
| 1 | Write(K,10,"a") | 10:a | — |
| 2 | Write(K,30,"b") | 10:a, 30:b | — |
| 3 | Write(K,20,"c") | 10:a, 20:c, 30:b | — |
| 4 | AsOf(K,15) | 10:a, 20:c, 30:b | "a"（ts≤15 最新为 10） |
| 5 | AsOf(K,30) | 10:a, 20:c, 30:b | "b"（含等于，ts=30 命中） |
| 6 | Delete(K,40) | 10:a, 20:c, 30:b, 40:删除 | — |
| 7 | AsOf(K,25) | 10:a, 20:c, 30:b, 40:删除 | "c"（ts≤25 最新为 20） |
| 8 | AsOf(K,40) | 10:a, 20:c, 30:b, 40:删除 | 不存在（最新为 tombstone） |

- (甲) 边界误为 `ts < T`：AsOf(K,30) 错成 "c"（漏掉 ts=30，正确 "b"）；AsOf(K,40) 错成 "b"（漏掉 ts=40 的删除，正确「不存在」）。
- (乙) 误取最早版本：AsOf(K,25) 错成 "a"（取 ts=10，正确 "c"）；AsOf(K,15) 仍为 "a"，与正确值巧合相同（≤15 只有一个版本）。
- (丙) 忽略 tombstone：AsOf(K,40) 错成 "b"（落到 ts=30，正确「不存在」）。

## 二、四条不变量的保证位置与钉住测试

1. 版本链不可变：`chain.Chain.Insert` 只往切片插入新 `ver.Version`，无更新/删除路径；测试 `TestImmutability`。
2. as-of 正确性：`chain.Chain.AsOf` 用 `sort.Search` 找 `ts<=T` 的最大者并判 tombstone；测试 `TestAsOfMatchesNaive`（对拍线性扫描）。
3. 快照一致：`api.Store.ViewAsOf` 逐 key 调同一 `AsOf`；测试 `TestViewAsOfMatchesReplay`（对拍过滤重放）。
4. 失败不留痕：`api.Store.Write/Delete` 先校验（`ErrEmptyKey`/`ErrNegativeTS`/`ErrEmptyValue`）再改状态；测试 `TestRejectedOpsLeaveNoTrace`。
