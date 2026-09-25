# NOTES — txid 幂等去重

## 第三节：六步推导

| 步 | 事件 | 已在已应用集? | 动作 | 本步后 state | 本步后已应用集 |
|---|---|---|---|---|---|
| 1 | Apply(1,k,+5) | 否 | 应用 | {k:5} | {1} |
| 2 | Apply(2,k,+3) | 否 | 应用 | {k:8} | {1,2} |
| 3 | Apply(1,k,+5) | 是 | 跳过 | {k:8} | {1,2} |
| 4 | Apply(3,k,+5) | 否 | 应用 | {k:13} | {1,2,3} |
| 5 | Apply(2,k,+3) | 是 | 跳过 | {k:13} | {1,2,3} |
| 6 | Apply(4,m,+2) | 否 | 应用 | {k:13,m:2} | {1,2,3,4} |

- (甲) 去重只看 txid：txid 3 不在已应用集，**应用**。若错按内容 (k,+5) 去重，第 4 步会被误判为重复而**跳过**，`state[k]` 错成 **8**（正确 13）。
- (乙) 查重与应用之间无原子性：两个 goroutine 都通过查重，操作被应用两次，`state[k]` 错成 **17**（正确 **15**，恰好应用一次）。
- (丙) 容量淘汰使 txid 1 丢失，`Restore({2,3,4})` 后已应用集缺 1；重投 `Apply(1,k,+5)` 被**再次应用**，`state[k]` 错成 **18**（正确 **13**）。违反不变量 1（不重复应用），并连带违反 2、3。

## 四条不变量的保证位置与钉住测试

1. 不重复应用：`apply.Applier.Apply` 在持锁临界区内「查重→应用→入集」原子完成；测试 `TestConcurrent`、`TestSixSteps`。
2. 与朴素参照一致：`api.Service.Apply` 委托同一临界区逻辑，逐首现 txid 应用；测试 `TestNaiveReference`。
3. 幂等：命中已应用集的分支只返回、不写任何状态（`apply.go` 中 `Seen` 为真时直接 `return false`）；测试 `TestIdempotentRepeat`。
4. 失败不留痕：`api.Service.Apply`/`Restore` 先做全部参数校验，任一非法即返回哨兵错误，未触达状态；测试 `TestRejectionNoTrace`。

查重 O(1)：`dedup.Set` 用 map 按 txid 直接定位，非导出计数器 `checked` 由白盒测试 `TestCheckCountConstant` 钉住。
