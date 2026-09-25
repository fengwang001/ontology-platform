# NOTES

## 第三节：八步推导（key=K）

| # | 操作 | 操作后 K 的版本链（ts:值，升序） | as-of 结果 |
|---|---|---|---|
| 1 | Write(K,10,"a") | 10:a | — |
| 2 | Write(K,30,"b") | 10:a, 30:b | — |
| 3 | Write(K,20,"c") | 10:a, 20:c, 30:b | — |
| 4 | AsOf(K,15) | 10:a, 20:c, 30:b | "a"（ts<=15 最大为 10） |
| 5 | AsOf(K,30) | 10:a, 20:c, 30:b | "b"（ts=30 含等于） |
| 6 | Delete(K,40) | 10:a, 20:c, 30:b, 40:删除 | — |
| 7 | AsOf(K,25) | 10:a, 20:c, 30:b, 40:删除 | "c"（ts<=25 最大为 20） |
| 8 | AsOf(K,40) | 10:a, 20:c, 30:b, 40:删除 | 不存在（最新可见是 tombstone） |

- (甲) 边界误为 `< T`：AsOf(K,30) 错成 "c"（应 "b"）；AsOf(K,40) 错成 "b"（应 不存在）。
- (乙) 误取最早版本：AsOf(K,25) 错成 "a"（应 "c"）；AsOf(K,15) 仍为 "a"，恰好不错。
- (丙) 忽略 tombstone：AsOf(K,40) 错成 "b"（应 不存在）。

## 第二节：四条不变量的保证位置与钉住测试

1. 版本链不可变：`chain.Insert` 只构造新 `ver.Version` 插入切片，从不改写已有元素；钉住：`TestImmutability`。
2. as-of 正确性：`chain.AsOf` 用 `sort.Search` 找 `ts<=T` 的前驱并判 tombstone；钉住：`TestAsOfMatchesNaive`（对拍朴素线性扫描）。
3. 快照一致：`api.ViewAsOf` 逐 key 调 `AsOf`；钉住：`TestViewAsOfConsistent`（对拍「过滤 ts<=T 后重放取最新」）。
4. 失败不留痕：`api.Write/Delete` 先校验（哨兵错误）再触碰状态；钉住：`TestRejectedOpsLeaveStateUnchanged`。
