# ontology-700 最少连接负载均衡 — 推导与不变量

## 三、八步推导（n=3，初始 (0,0,0)）

| 步 | 操作 | 操作后 (c0,c1,c2) | 结果 |
|---|---|---|---|
| 1 | Acquire(0) | (1,0,0) | 成功 |
| 2 | Acquire(2) | (1,0,1) | 成功 |
| 3 | Pick() | (1,0,1) | 返回 1（c1=0 唯一最少） |
| 4 | Acquire(1) | (1,1,1) | 成功 |
| 5 | Pick() | (1,1,1) | 返回 0（三台并列，取下标最小） |
| 6 | Release(0) | (0,1,1) | 成功 |
| 7 | Pick() | (0,1,1) | 返回 0（c0=0 最少） |
| 8 | Release(0) | (0,1,1) | 错误：下溢，状态不变 |

- (甲) 第 5 步 (1,1,1) 并列，正确返回 **0**；若并列取下标最大则返回 **2**。
- (乙) 第 7 步正确返回 **0**；若比较写反选连接数最多者，(0,1,1) 中最多为 c1=c2=1，取下标最小得 **1**。
- (丙) 第 8 步 c0 已为 0，正确报错且不变；若无下溢校验 c0 变 **-1**，此后 Pick 会把 -1 当最少而永远选 0，计数非负不变量被破坏。

## 二、四条不变量：保证位置与钉住测试

1. 与朴素参照一致：lc 用 (count,index) 字典序最小堆，堆顶即朴素扫描解（lc/lc.go `Pick`）；测试 `TestPickMatchesNaive`（lc/lc_test.go）、`TestNaiveConsistency`（api/api_test.go）。
2. 计数非负：`Release` 下溢校验先于修改（svc/svc.go `Release`）；测试 `TestUnderflow`（api/api_test.go）。
3. 守恒：仅成功的 Acquire/Release 各 ±1，失败不改（svc/svc.go）；测试 `TestConservation`（api/api_test.go）。
4. 失败不留痕：一切校验先于任何修改（svc/svc.go `Acquire`/`Release`）；测试 `TestFailureNoTrace`（api/api_test.go）。
