package fulfillment

import "fmt"

// Tier 延误档位：延误达到 Threshold（取等归高档）即赔付 Amount。
type Tier struct {
	Threshold int64
	Amount    int64
}

// Params 构造参数。订单被接受时整体快照，之后变更不影响已接受订单。
type Params struct {
	PromiseDuration   int64  // 承诺时长
	PrepAllowance     int64  // 商家出餐容许时长
	DispatchAllowance int64  // 平台派单容许时长
	PickupAllowance   int64  // 骑手取货容许时长
	AddressExtension  int64  // 用户改址固定延展量
	ExtensionCap      int64  // 延展累计上限
	ClaimWindow       int64  // 赔付申请窗口
	Tiers             []Tier // 严格递增的延误档位
}

func (p Params) validate() error {
	if p.PromiseDuration <= 0 {
		return fmt.Errorf("%w: promise duration must be positive", ErrInvalidParam)
	}
	if p.PrepAllowance < 0 || p.DispatchAllowance < 0 || p.PickupAllowance < 0 {
		return fmt.Errorf("%w: allowances must be non-negative", ErrInvalidParam)
	}
	if p.AddressExtension < 0 {
		return fmt.Errorf("%w: address extension must be non-negative", ErrInvalidParam)
	}
	if p.ExtensionCap < 0 {
		return fmt.Errorf("%w: extension cap must be non-negative", ErrInvalidParam)
	}
	if p.ClaimWindow <= 0 {
		return fmt.Errorf("%w: claim window must be positive", ErrInvalidParam)
	}
	if len(p.Tiers) == 0 {
		return fmt.Errorf("%w: tiers must be non-empty", ErrInvalidParam)
	}
	for i, t := range p.Tiers {
		if t.Threshold <= 0 {
			return fmt.Errorf("%w: tier threshold must be positive", ErrInvalidParam)
		}
		if i > 0 && t.Threshold <= p.Tiers[i-1].Threshold {
			return fmt.Errorf("%w: tier thresholds must be strictly increasing", ErrInvalidParam)
		}
		if t.Amount < 0 {
			return fmt.Errorf("%w: tier amount must be non-negative", ErrInvalidParam)
		}
	}
	return nil
}

func (p Params) clone() Params {
	q := p
	q.Tiers = append([]Tier(nil), p.Tiers...)
	return q
}
