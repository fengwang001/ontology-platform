# NOTES

## 一、八步推导（capacity = 5）

| # | 操作 | 数量 | 结果 |
|---|---|---|---|
| 1 | Produce(3) | 3 | 成功 |
| 2 | Produce(2) | 5 | 成功（恰好填满，`>` 才拒绝） |
| 3 | Produce(1) | 5 | 背压（5+1>5，不放任何元素） |
| 4 | Consume(2) | 3 | 成功 |
| 5 | Produce(3) | 3 | 背压（空闲 2 < 需求 3） |
| 6 | Consume(1) | 2 | 成功 |
| 7 | Produce(3) | 5 | 成功（2+3=5 恰好填满） |
| 8 | Consume(6) | 5 | 错误：下溢（状态不变） |

- (甲) 第 5 步若部分放入（容纳 2、丢弃 1）：数量会错成 **5**（正确为 3）。
- (乙) 判满误写成 `>= capacity`：第 2 步 3+2=5 会被错判成**背压/队列满而拒绝**，数量停在 3（正确应放行至 5）。
- (丙) 第 8 步若不校验下溢：数量变成 5-6 = **-1**（正确保持 5 并返回下溢错误）。

## 二、四条不变量：保证位置与钉住测试

1. 与朴素参照一致：`api/api.go` 的 `SelfCheck` 内置序列与显式切片逐元素 append/弹出比对；测试 `TestNaiveReference`、`TestSelfCheck` 钉住。
2. 容量不越界：`bq/bq.go` 的 `TryProduce` 在锁内判定 `count+n > capacity` 即整体拒绝；测试 `TestInvariants` 每步断言 `0<=Count<=capacity`。
3. 守恒：`bq/bq.go` 中仅 `TryProduce`/`TryConsume` 成功路径改写 `count`（分别 +n/-n）；测试 `TestInvariants` 用 Σ成功 Produce−Σ成功 Consume 对账。
4. 失败不留痕：`bp/bp.go` 的 `NewController`/`Produce`/`Consume` 在调用 bq 前先做哨兵校验，bq 两个 Try 方法失败路径不改 `count`；测试 `TestRejectedOpsNoTrace` 钉住。
