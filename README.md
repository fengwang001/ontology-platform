# 证券交割日终批处理系统

Go 实现的券款对付（DVP）日终结算库，支持部分交割、失败滚动、日罚金、到期强制了结和卖方补偿。

## 能力

- 营业日严格顺序批处理，拒绝跳日、重复日、倒退日和非营业日。
- 指令按编号升序处理，判定统一基于当日批首头寸。
- 同批内按指令累计占用券和现金；收到的券款次日才可使用。
- 支持允许或禁止部分交割；不允许部分时只能全额成交或零交割。
- 失败责任逐日判定，券款两边都不足时归卖方。
- 罚金按基点向上取整，独立记录应付/应收，不改变现金头寸。
- 累计失败营业日数达到 `B` 时当日计罚后强制了结；卖方责任按参考价差补偿。
- 查询账户头寸、罚金、补偿、指令状态和累计失败日数，查询返回快照。
- 所有写操作互斥串行，查询使用读锁；相同操作序列可精确复现。
- 日终处理不扫描历史已了结指令。

## 快速开始

```go
package main

import (
	"fmt"

	"ontology/settlement"
)

func main() {
	system, err := settlement.New(
		settlement.Config{
			BusinessDays: []int{1, 2, 3},
			MaxFailDays:  2,
			PenaltyBPS:   100,
		},
		[]settlement.Account{
			{ID: "buyer", Cash: 100},
			{ID: "seller", Holdings: map[settlement.SecurityID]int64{"X": 7}},
		},
	)
	if err != nil {
		panic(err)
	}
	system.SetLogger(nil)

	err = system.RegisterOrder(settlement.OrderInput{
		ID:            1,
		Security:      "X",
		Buyer:         "buyer",
		Seller:        "seller",
		Quantity:      10,
		Price:         10,
		SettlementDay: 1,
		AllowPartial:  true,
	})
	if err != nil {
		panic(err)
	}

	result, err := system.ProcessBusinessDay(1, map[settlement.SecurityID]int64{"X": 10})
	if err != nil {
		panic(err)
	}

	fmt.Println(result.Processed[0].Delivered) // 7
	fmt.Println(result.Penalties[0].Amount)    // ceil(3*10*100/10000) = 1
}
```

## 主要 API

### `New(config, accounts)`

创建系统。营业日必须非空且不重复，`MaxFailDays > 0`，`PenaltyBPS >= 0`。账户现金和持仓必须非负，初始总现金及每种券总量不能超过 `int64`。

### `RegisterOrder(input)`

登记成交指令。编号唯一，数量和单价为正，买方、卖方、证券不能为空，买卖双方不得相同，应交割日必须是营业日。已完成最近营业日之后，不能再登记应交割日更早的指令。

错误优先级：

1. `ErrInvalidParameter`
2. `ErrDuplicateOrder`
3. `ErrAccountNotFound`
4. `ErrDatePassed`

### `ProcessBusinessDay(day, referencePrices)`

执行一个营业日的日终批处理。候选指令为应交割日不晚于当日且尚未了结的指令，按编号升序处理。返回的 `ProcessResult` 包含：

- `Processed`：请求量、可用券、可买数量、实际交割量、剩余量和失败责任方；
- `Penalties`：责任方、对手方、剩余量、现金额、罚率和罚金；
- `ForceClosures`：强制了结责任方、参考价和补偿金额。

错误优先级：

1. `ErrInvalidParameter`
2. `ErrNonBusinessDay`
3. `ErrOutOfOrder`

### 查询

- `Position(accountID)`：现金和证券持仓快照；
- `PenaltyBalances(accountID)`：罚金应付和应收；
- `CompensationBalances(accountID)`：补偿应付和应收；
- `Order(id)`：指令不可变输入、已交割量、剩余量、状态和累计失败日数。

指令状态包括：

- `pending`：待交割；
- `partial`：部分交割；
- `completed`：已完成；
- `forced`：已强制了结。

## 日志

默认日志输出到标准错误，记录登记输入、批处理输入、每条指令的判定依据、罚金、强制补偿和批处理输出。可调用 `SetLogger` 注入自定义 `Logger`，传 `nil` 可关闭日志。

## 测试

```bash
go test ./...
go test -race ./...
go test -v ./settlement
go test -run TestRandomOperationsAgainstNaiveModel -v ./settlement
go vet ./...
```

如果当前 shell 找不到 Go：

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/ontology-gocache
```

测试覆盖需求中的所有边界，并用一个独立朴素模型对 200 组随机操作序列逐日逐账户逐指令对照。随机和关键用例使用 `testing.T.Logf` 输出输入、输出和判定依据。

## 文档

- 设计说明：`docs/design.md`
- 核心实现：`settlement/`
- 规则测试：`settlement/settlement_test.go`
- 朴素模型与随机对照：`settlement/naive_model_test.go`
- 并发、守恒和复杂度验证：`settlement/nonfunctional_test.go`
