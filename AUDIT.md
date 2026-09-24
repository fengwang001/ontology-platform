# AUDIT: 五条不变量的保证位置与钉住它们的测试

## 不变量 1：绝不低估（Estimate(x) >= true(x)）

- 位置：`sketch/sketch.go` 的 `Add`（d 格各加 n，精确求和）与 `Estimate`
  （取 d 格最小值；每格 >= true(x)，最小值亦然）。不提供 Sub 与保守更新
  （推导见 DESIGN.md 第 1、2 节）。
- 测试：`TestNeverUnderestimate`、`TestStreamErrorsAndSelfCheck`、demo 第 2 行。

## 不变量 2：合并同构

- 位置：`sketch/sketch.go` 的 `Merge` 逐格相加；普通更新可交换可结合。
  参数不同返回 `ErrIncompatible`，校验先于写入，两边都不变。
- 测试：`TestDeterminismAndMerge`（`Equal` 逐格比对）、
  `TestRejectedOpsLeaveNoTrace` 的 incompatible 用例、demo 第 4-5 行。

## 不变量 3：确定性

- 位置：`hashfam/hashfam.go`——salt 由行号经 splitmix64 确定导出，哈希为
  FNV-1a 纯函数；无随机源、无时间、无 map 迭代序。
- 测试：`TestDeterminismAndMerge`（两次独立构建 `Equal`）、demo 第 1、3 行。

## 不变量 4：频繁项不漏

- 位置：`topk/topk.go` 的 `HeavyHitters` 用 `Estimate >= threshold` 判定
  （DESIGN.md 第 4 节：估计只偏高，真超阈值者必入选）；`sort.Strings` 定序。
- 测试：`TestHeavyHittersNoFalseNegatives`（含排序断言）、demo 第 11 行。

## 不变量 5：失败不留痕

- 位置：`sketch/sketch.go` 的 `Add`/`Merge` 先完成全部校验（空键、上限、
  参数匹配）再写格子；`stream/stream.go` 仅在 sketch 接受后才动候选集。
- 测试：`TestRejectedOpsLeaveNoTrace`（每种拒绝后 `Equal`(克隆) 逐格比对，
  且超限拒绝后仍可继续 Add）、demo 第 5-6、13 行。

## 访问格子数实测（第四节）

`TestAccessCounter`（d=4）与 `TestQueriesProportionalToCandidates` 实测：

| 操作 | w=1000 | w=100000 |
| --- | --- | --- |
| Add 访问格子数 | 4 | 4 |
| Estimate 访问格子数 | 4 | 4 |
| HeavyHitters 估计次数（5 个候选） | 5 | 5 |

访问数恰等于深度 d（或候选数），不随 w 变化：未扫描整行、未触碰 w*d 格。
计数器为非导出字段（`sketch.accessed`、`topk.queries`），不在公开接口。

## 并发

- 只读路径不写共享状态；计数器用 `sync/atomic`，`go test -race` 干净。
- 测试：`TestConcurrentReads`、`TestConcurrentQueriesIdentical`（16 个
  goroutine 经 barrier 同时查询，结果逐位相同，无 sleep）、demo 第 14 行。
