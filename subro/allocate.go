package subro

// recoverableCap returns floor(totalLoss * ratioBP / 10000) computed
// without overflowing int64 for any non-negative inputs.
func recoverableCap(totalLoss, ratioBP int64) int64 {
	q, r := totalLoss/10000, totalLoss%10000
	return q*ratioBP + r*ratioBP/10000
}

// allocate computes the entitled amounts for the three parties from
// aggregate inputs only. It is pure and order-independent.
//
//	distributable = min(netTotal, cap)
//	insured       = min(distributable, uncompensated), or 0 if waived
//	insurer       = min(distributable-insured, insurerPaid)
//	thirdParty    = netTotal - insured - insurer (excess, refunded)
func allocate(netTotal, cap, uncompensated, insurerPaid int64, waived bool) Entitlement {
	distributable := min(netTotal, cap)
	var insured int64
	if !waived {
		insured = min(distributable, uncompensated)
	}
	insurer := min(distributable-insured, insurerPaid)
	return Entitlement{
		Insured:    insured,
		Insurer:    insurer,
		ThirdParty: netTotal - insured - insurer,
	}
}
