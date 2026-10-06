package reinsurance

import "math/big"

func validateTerms(t Terms) error {
	if t.QuotaPercent < 1 || t.QuotaPercent > 99 {
		return ErrInvalidArgument
	}
	if t.SurplusLine < 0 {
		return ErrInvalidArgument
	}
	if t.SurplusLines <= 0 {
		return ErrInvalidArgument
	}
	if t.XLDeductible < 0 || t.XLLimit < 0 {
		return ErrInvalidArgument
	}
	if t.XLReinstatements < 0 {
		return ErrInvalidArgument
	}
	if mulOverflow(t.SurplusLine, int64(t.SurplusLines)) {
		return ErrInvalidArgument
	}
	return nil
}

func registerPolicy(p *Policy, t Terms) error {
	if p == nil || p.ID == "" || p.Limit <= 0 || p.StartDay < 0 || p.EndDay < 0 || p.StartDay >= p.EndDay {
		return ErrInvalidArgument
	}
	quota := mulDivFloor(p.Limit, int64(t.QuotaPercent), 100)
	remainder := p.Limit - quota
	capacity := t.SurplusLine * int64(t.SurplusLines)
	if remainder <= t.SurplusLine {
		p.QuotaShare = quota
		p.SurplusShare = 0
		p.NetRetention = remainder
	} else {
		surplus := remainder - t.SurplusLine
		if surplus > capacity {
			return ErrCapacityExceeded
		}
		p.QuotaShare = quota
		p.SurplusShare = surplus
		p.NetRetention = t.SurplusLine
	}
	if p.QuotaShare+p.SurplusShare+p.NetRetention != p.Limit {
		panic("reinsurance: shares must partition the limit")
	}
	return nil
}

func mulOverflow(a, b int64) bool {
	if a == 0 || b == 0 {
		return false
	}
	r := a * b
	return r/a != b
}

func mulDivFloor(a, b, c int64) int64 {
	r := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	r.Quo(r, big.NewInt(c))
	return r.Int64()
}
