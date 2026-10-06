# leasechain：租赁转租链与责任传递管理服务

纯 Go（标准库）实现，无外部依赖。覆盖主租约、多级转租、房东同意的授予/撤回、
期限与租金约束、欠费责任沿链回溯、追偿权、终止级联与独立承认后的提升、承租人退出。

## 核心 API（`Service`）

- `New(Config{P,D,G,PayDay})`：P 为租金倍数（整数百分比）、D 为链深度上限、G 为逾期宽限天数、PayDay 为月付款日（1..28）。
- `CreateMaster(now, landlord, tenant, start, end, rent)`：签订主租约（半开区间 `[start,end)`）。
- `GrantGeneral/RevokeGeneral(now, landlord, tenant)`：概括性同意授予/撤回。
- `GrantOneTime(now, landlord, tenant, parent, start, end, rent)`：一次性同意（绑定确切条款）。
- `Sublease(now, parent, tenant, start, end, rent)`：发起转租。
- `Recognize(now, lease, landlord)`：对当前有效租约授予独立承认。
- `Terminate(now, lease, caller)`：主动终止（承租人或房东），触发级联。
- `Exit(now, lease, tenant)`：承租人退出（有未承认有效下级则拒绝）。
- `Advance(now)`：推进时钟并结算（生成账期/欠费、到期级联）。
- `PayArrear(now, arrear, payerLease, amount)`：欠费租约自身或任一上级清偿，形成等额追偿权。
- `ResponsibleChain(arrear)`：返回责任人链与实际沿链步数（仅与链深度有关）。
- `GetLease/GetArrear/RecourseParties`：只读查询。

## 错误与次序

所有变更操作按固定次序只返回第一个错误：参数非法 → 时钟回退 → 租约不存在或已终止 →
未取得同意 → 期限越出上级 → 租金超过倍数上限 → 链深度超限 → 状态不允许（含已有有效下级、
重复承认、对自身转租、欠费已清偿再清偿、越权等）。被拒操作通过深拷贝事务整体回滚，
不改动任何租约、同意、欠费、追偿与时钟。

## 关键语义

- 月账期按整数日映射 UTC 日历；起租月应付日不早于起租日，应付日 `due` 满足 `now>=due+G` 才产生欠费。
- 次租约期限必须落在上级 `[start,end]` 内（终止日相等允许）；租金满足 `rent*100<=上级rent*P`（相等允许）。
- 一次性同意用一次即失效；概括性同意撤回只影响其后发起的转租，已成立次租约不受影响。
- 上级终止时，已取得独立承认的有效下级提升为上级父级的下级（根则直接对房东），期限租金不变；
  未承认下级随级联终止，终止日取上级终止日。
- 退出不消灭既有欠费责任与追偿权。

## 并发与确定性

单一互斥配合深拷贝事务，使所有并发调用等价于某一串行顺序；相同操作序列重放得到完全相同的
链结构、欠费与追偿记录。

## 测试

- `scenarios_test.go`：终止日恰等、租金恰等/超一分、一次性同意只用一次、概括同意撤回前后、
  深度恰等 D 与超一级、逾期恰达 G 天、多级清偿归属与追偿、级联与承认后提升、退出拒/放、
  拒绝次序、被拒不留痕、退出后责任保留。
- `diff_test.go` + `fuzzgen_test.go` + `naive.go`：与独立编写的朴素模型做 40 组随机序列对照，
  `-v` 逐步打印输入、输出（错误分类）与判定依据，并逐笔比对全量状态。
- `complexity_concurrency_test.go`：责任人查询步数只随深度（N=10 与 N=2000 步数相同）；
  `-race` 下并发转租与并发清偿的唯一性；清偿总额=追偿总额不变量。

## 运行

```bash
go test ./...          # 全量（功能/随机对照/不变量）
go test -race ./...    # 竞态与并发
go test -v -run TestRandomDifferential ./...   # 打印每步输入/输出/判定依据
go vet ./...
```

设计取舍与被放弃方案见 `DESIGN.md`。
