# NOTES — 键控状态的有界版本历史清理

## 第三节推导：K=3，同一 Key 六次 Put 分步表

| 步 | 操作 | 分配 v | 本步后物理保留 (v:value) | 触发清理？ |
|---|---|---|---|---|
| 1 | Put("a") | 1 | [1:a] | 否（1 <= 3） |
| 2 | Put("b") | 2 | [1:a, 2:b] | 否（2 <= 3） |
| 3 | Put("c") | 3 | [1:a, 2:b, 3:c] | 否（3 > 3 不成立） |
| 4 | Put("d") | 4 | [2:b, 3:c, 4:d] | 是，物理删除 v=1 |
| 5 | Put("e") | 5 | [3:c, 4:d, 5:e] | 是，物理删除 v=2 |
| 6 | Put("f") | 6 | [4:d, 5:e, 6:f] | 是，物理删除 v=3 |

- (甲) 第 3 步后保留 3 个版本，未删任何版本。若清理条件错写成「版本数 >= K」，第 3 步会错删 v=1，`GetAt(1)` 会错成「不存在」（正确应命中 "a"）。
- (乙) 第 4 步后最旧保留版本是 v=2，`GetAt(2)` 应命中 "b"。若命中条件错写成「v > 最旧保留版本」（严格大于），`GetAt(2)` 会错成「不存在」。
- (丙) 第 4 步后 `GetAt(1)` 应为「不存在」（v=1 已物理删除）。若清理是惰性的（Put 只标记、GetAt 按物理存在判断），v=1 仍物理存在，`GetAt(1)` 会错成命中 "a"。

## 四条不变量：保证位置与钉住测试

1. 与朴素参照一致：`hist.Hist.Get` 恒取切片尾（最新），`store` 按 Key 隔离；测试 `TestReplayConsistency`（api/api_test.go）。
2. 有界 + 最新：`hist.Hist.Put` 追加后当 `len > K` 立即从头部 O(1) 删除最旧版本，版本号 `maxV` 只增不复用；测试 `TestRetentionBounds`（api/api_test.go）、`TestCleanupMoveBounded`（hist/hist_test.go）。
3. 可见性自洽：`hist.Hist.GetAt` 命中条件 `oldest <= v <= maxV` 且按物理切片定位；测试 `TestVisibilityBoundary`（api/api_test.go）。
4. 失败不留痕：`api` 在入口先校验（空 Key / K<=0 / version<=0），校验失败在触碰 `store` 前返回哨兵错误；测试 `TestRejectedOpsNoStateChange`（api/api_test.go）。
