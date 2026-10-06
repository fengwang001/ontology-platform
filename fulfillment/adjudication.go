package fulfillment

// Verdict 赔付裁决，一经作出不可更改。
type Verdict struct {
	OrderID     string
	Promise     int64 // 延展后的承诺送达时刻
	DeliverTs   int64
	Delay       int64
	Tier        int // 命中档位下标，未达最低档为 -1
	Amount      int64
	Responsible Party
	Auto        bool // 是否平台自动裁决
}

// tierFor 按阈值划分档位：延误恰等于阈值归该阈值对应档（取等归高档），
// 超过最高阈值归最高档；未达最低阈值返回 -1, 0。
func tierFor(delay int64, tiers []Tier) (int, int64) {
	idx, amount := -1, int64(0)
	for i, t := range tiers {
		if delay < t.Threshold {
			break
		}
		idx, amount = i, t.Amount
	}
	return idx, amount
}

// adjudicate 裁决并冻结结果。赔付由责任方承担，用户为责任方时不赔付。
func adjudicate(o *order, auto bool) Verdict {
	delay := o.delay()
	tier, amount := tierFor(delay, o.params.Tiers)
	resp := attribute(o).responsible()
	if resp == User {
		amount = 0
	}
	v := Verdict{
		OrderID:     o.id,
		Promise:     o.promise(),
		DeliverTs:   o.deliverTs,
		Delay:       delay,
		Tier:        tier,
		Amount:      amount,
		Responsible: resp,
		Auto:        auto,
	}
	o.settled = true
	o.verdict = v
	return v
}
