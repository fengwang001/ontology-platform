# reinsurance 包

财产险再保险分出引擎：成数 + 溢额 + 事故超赔三层叠加，保证每笔赔款在
本公司、成数合约、溢额合约、超赔层之间的归属唯一、守恒、可精确复现。

## 核心入口

- `NewEngine(Terms)`：按年度条款构造引擎，条款非法返回可 `errors.Is` 区分的错误。
- `(*Engine).RegisterPolicy(Policy)`：登记保单并固化保额分出结构；超能力拒绝。
- `(*Engine).FileClaim(Claim)`：登记赔款，返回成数/溢额/净自留三段切分。
- `(*Engine).CancelClaim(id)`：撤销赔款，结果等价于该赔款从未存在。
- `(*Engine).Snapshot()`：保单、按 `(时刻, 编号)` 排序的事故超赔结果与各方合计。
- `NewNaiveModel(Terms).Compute(...)`：独立朴素模型，按事故时刻一次性算成，供对照。

## 规则要点

- 金额单位为非负整数分；承保区间为 `[StartDay, EndDay)`，事故时刻换算为秒比较。
- 成数后剩余 `<=` 自留额不分溢额；溢额部分超过 `自留额*线数` 整单拒保。
- 切分向下取整，尾差归净自留；超赔按事故编号聚合净自留，恰等于自留点不触发。
- 总层能力 `层限*(恢复次数+1)`，按时刻先后、同刻按编号字典序消耗。
- 晚报早事故或撤销会从插入点起重算，结果与全量一次算成逐分相等。

## 验证

```bash
go test ./...
go test -race ./...
go test -run TestRandomEquivalence -v   # 随机到达/撤销 vs 朴素模型
go test -bench . -benchmem             # O(1) 切分，不随保单总数增长
```
