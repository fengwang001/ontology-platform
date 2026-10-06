package reinsurance

func splitClaim(p *Policy, amount int64) (quota, surplus, net int64) {
	quota = mulDivFloor(amount, p.QuotaShare, p.Limit)
	surplus = mulDivFloor(amount, p.SurplusShare, p.Limit)
	net = amount - quota - surplus
	return
}
