# NOTES: bounded queue + backpressure

capacity=5，数量均为操作后：

| # | 操作 | 结果 | 数量 |
|---|---|---|---|
| 1 | Produce(3) | 成功 | 3 |
| 2 | Produce(2) | 成功（3+2==5，恰好填满，`>` 才拒） | 5 |
| 3 | Produce(1) | 背压（5+1>5，不放任何元素） | 5 |
| 4 | Consume(2) | 成功 | 3 |
| 5 | Produce(3) | 背压（3+3>5，不放任何元素） | 3 |
| 6 | Consume(1) | 成功 | 2 |
| 7 | Produce(3) | 成功（2+3==5） | 5 |
| 8 | Consume(6) | 错误（下溢，6>5，数量不变） | 5 |

(甲) 部分放入 2 个、丢弃 1 个：数量错成 3+2=**5**（正确为 3）。
(乙) 判满写成 `>=capacity`：第 2 步 3+2==5 被**错判为背压**而拒绝，数量停在 3（第 7 步同样遭误拒）。
(丙) 不校验下溢：5-6=**-1**，数量错成 -1。

不变量 —— 代码位置 / 钉住的测试：

1. 与朴素参照一致：环形缓冲只动 head/count（`bq/bq.go`）；`api.SelfCheck` 与 `TestRandomMatchesNaive` 钉住。
2. 容量不越界 0<=Count<=cap：`bq.Produce` 的 `count+n>cap` 判定（`bq/bq.go`）；`TestBounds`、`TestConcurrentProduce` 钉住。
3. 守恒：仅在成功路径 `count+=n`/`-=n`（`bq/bq.go`）；`TestConservation`、`SelfCheck` 与 `TestRandomMatchesNaive` 钉住。
4. 失败不留痕：非法参数/容量在触达核心前由 `bp/bp.go`、`api/api.go` 拒绝；`TestRejectedOpsLeaveNoTrace` 钉住。
