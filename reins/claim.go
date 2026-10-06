package reins

// Split 是一笔赔款在各方之间的最终归属。
type Split struct {
	QuotaShare int64 // 成数合约承担
	Surplus    int64 // 溢额合约承担
	XLRecover  int64 // 事故超赔层承担
	FinalNet   int64 // 本公司最终自留
}

type claim struct {
	no, policyNo, eventID string
	time                  int64
	amount                int64
	qs, surplus, net      int64
	xlRecovered           int64
}

func (c *claim) split() Split {
	return Split{
		QuotaShare: c.qs,
		Surplus:    c.surplus,
		XLRecover:  c.xlRecovered,
		FinalNet:   c.net - c.xlRecovered,
	}
}

// splitClaim 按保额基准的分出结构同比例切分，分向下取整，尾差归净自留。
func splitClaim(p *policy, amount int64) (qs, sur, net int64) {
	qs = amount * p.ces.QuotaShare / p.sumInsured
	sur = amount * p.ces.Surplus / p.sumInsured
	net = amount - qs - sur
	return
}
