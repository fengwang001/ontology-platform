# 运费计算与结算系统 — 使用文档

面向 `freight/system` 门面的最小用法示例。完整规则见 `docs/design.md`。

```go
package main

import (
	"fmt"
	"os"

	"ontology/freight/model"
	"ontology/freight/system"
)

func main() {
	// 传入日志器即可逐操作打印 JSON 审计行；传 nil 表示静默。
	s := system.New(system.NewJSONLogger(os.Stdout))

	c := &model.Contract{
		ID: "C-001", CarrierID: "顺丰",
		Route:       model.Route{From: "北京", To: "上海"},
		Class:       model.Standard,
		EffectiveAt: 100, ExpiresAt: 200, // [100,200) 左闭右开

		VolumeFactor: 1000, BillingUnit: 1000, // 重量克，尺寸毫米，金额分
		Tiers: []model.WeightTier{
			{Lower: 0, Upper: 10_000, PricePerUnit: 100},
			{Lower: 10_000, Upper: 0, PricePerUnit: 80},
		},
		FuelRatePerMille:    100, // 100‰ = 10%
		RemoteRegions:       []model.Region{"西藏"},
		RemoteSurcharge:     500,
		SideThreshold:       1000,
		WeightThreshold:     40_000,
		OversizeSurcharge:   700,
		OverweightSurcharge: 900,
		MinimumCharge:       2000,
		MaxWeight:           100_000,
		MaxSide:             2000,
	}
	if err := s.AddContract(c); err != nil {
		panic(err)
	}

	w := &model.Waybill{
		Number: "W-001", CarrierID: "顺丰",
		Route: model.Route{From: "北京", To: "上海"},
		Class: model.Standard,
		// 用揽收时刻选合同，而不是调用时刻
		PickupAt: 150,
		Weight:   12_000,
		Dim:      model.Dimensions{Length: 300, Width: 400, Height: 500},
	}
	fb, err := s.Price(w)
	if err != nil {
		panic(err)
	}
	fmt.Println("合计(分):", fb.Total)

	// 结算一次：金额固化；再次结算返回 model.CodeAlreadySettled。
	if _, err := s.Settle("W-001"); err != nil {
		panic(err)
	}

	// 多承运商询价：成功项在前（总价、承运商升序），失败项带原因码单列。
	lines, _ := s.Quote(system.QuoteRequest{
		Route:    model.Route{From: "北京", To: "上海"},
		Class:    model.Standard,
		PickupAt: 150,
		Weight:   12_000,
		Dim:      model.Dimensions{Length: 300, Width: 400, Height: 500},
		// Carriers 留空表示该线路下全部已知承运商
	})
	for _, ln := range lines {
		if ln.Reason == "" {
			fmt.Println(ln.CarrierID, ln.Total)
		} else {
			fmt.Println(ln.CarrierID, "失败:", ln.Reason)
		}
	}
}
```

## 错误码

| 错误码 | 触发场景 |
| --- | --- |
| `参数非法` | 合同/运单字段不合法；未计价直接结算 |
| `无合同` | 指定承运商的该线路+等级没有任何合同 |
| `时刻未覆盖` | 有合同，但揽收时刻落在所有生效区间之外 |
| `超出承运范围` | 实际重量超 `MaxWeight` 或实际单边超 `MaxSide` |
| `区间重叠` | 新增/修订合同与同车道已有合同生效区间重叠（相接允许） |
| `已结算` | 同一运单号第二次结算 |

用 `model.CodeOf(err)` 取错误码做分支判断。
