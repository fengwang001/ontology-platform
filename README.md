# 快递智能柜

Go 实现的快递智能柜格口寄存与取件系统，覆盖投递、取件、滞留费、超时回收、运营解锁、确定性取件码、并发串行化和常数时间格口选择。

## 快速开始

```go
package main

import (
	"fmt"
	"log"

	smartlocker "ontology"
)

func main() {
	locker, err := smartlocker.NewLocker(smartlocker.Config{
		Cells: map[string]smartlocker.Size{
			"s1": smartlocker.Small,
			"m1": smartlocker.Medium,
			"l1": smartlocker.Large,
		},
		FeePolicy: smartlocker.FeePolicy{
			FreeDuration: 10 * 60,
			Period:       60,
			PeriodFee:    1,
			MaximumFee:   20,
		},
		StorageLimit: 24 * 60 * 60,
		CodeCooldown: 5 * 60,
		Logger:       log.Default(),
	})
	if err != nil {
		panic(err)
	}

	deposit, err := locker.Deposit(0, smartlocker.Parcel{
		Waybill: "SF123",
		Phone:   "13800001234",
		Size:    smartlocker.Small,
	})
	if err != nil {
		panic(err)
	}

	if _, err := locker.Pay(601, "SF123", 1); err != nil {
		panic(err)
	}
	pickup, err := locker.Pickup(601, deposit.Code, "13800001234")
	if err != nil {
		panic(err)
	}
	fmt.Println(pickup.Waybill, pickup.CellID)
}
```

## API

- `NewLocker(cfg Config) (*Locker, error)`：校验配置并建立格口索引。
- `Deposit(at int64, parcel Parcel) (DepositResult, error)`：按最小可容纳规格、同规格最小编号分配格口。
- `Pickup(at int64, code, phone string) (PickupResult, error)`：按规定优先级校验取件码、锁定、手机号后四位、超时和费用。
- `Pay(at int64, waybill string, amount int64) (PaymentResult, error)`：缴纳指定时刻的新增应缴费用，金额必须恰好相等。
- `Recycle(at int64, waybill string) (RecycleResult, error)`：仅允许运营回收已超时快件。
- `Unlock(at int64, waybill string) (UnlockResult, error)`：运营解锁并清零错误次数。
- `Snapshot() Snapshot`：读取当前快件状态，用于测试或运营后台。
- `AllocationStats() AllocationStats`：读取格口选择的可观测探针统计。

所有时间参数都是非负整数秒。成功操作要求时刻不小于上一次成功操作时刻；手机号后四位不符只累计错误次数，不推进时钟。

## 错误码

- `invalid_argument`
- `clock_rewound`
- `duplicate_waybill`
- `no_compatible_cell`
- `all_fit_cells_occupied`
- `code_not_found`
- `parcel_locked`
- `phone_mismatch`
- `parcel_timed_out`
- `fee_due`
- `wrong_payment_amount`
- `parcel_not_timed_out`

可通过 `errors.As(err, &operationErr)` 取得稳定的 `Code`。

## 设计与测试

详细模块边界、算法证明、取舍和被放弃方案见 `docs/DESIGN.md`。

测试覆盖：

- 规格恰好相等与向上差一档；
- 取件码冷却恰好到期与差一秒；
- 免费保管、计费周期和费用封顶边界；
- 保管上限恰好到期与差一秒；
- 连续三次手机号错误锁定、运营解锁、成功取件清零；
- 被拒绝操作不改状态，手机号错误例外只累计错误次数；
- 并发取件恰好一次成功；
- 300 组固定种子随机序列与独立朴素模型对照。

## 本地验证

```bash
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test -v ./...
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go vet ./...
/usr/local/go/bin/gofmt -l .
```
