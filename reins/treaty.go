package reins

// Treaty 描述合约年度内同时生效的三份合约条款，金额单位均为分。
type Treaty struct {
	QuotaSharePercent int   // 成数分出比例，1..99
	SurplusRetention  int64 // 溢额自留额
	SurplusLines      int   // 溢额线数，正整数
	XLRetention       int64 // 事故超赔每次事故自留点
	XLLimit           int64 // 事故超赔层限额
	Reinstatements    int   // 恢复次数，非负
}

func (t Treaty) validate() error {
	if t.QuotaSharePercent < 1 || t.QuotaSharePercent > 99 {
		return &Error{CodeInvalidParam, "参数非法: 成数比例越界"}
	}
	if t.SurplusLines <= 0 {
		return &Error{CodeInvalidParam, "参数非法: 线数非正"}
	}
	if t.Reinstatements < 0 {
		return &Error{CodeInvalidParam, "参数非法: 恢复次数为负"}
	}
	if t.SurplusRetention < 0 || t.XLRetention < 0 || t.XLLimit < 0 {
		return &Error{CodeInvalidParam, "参数非法: 金额为负"}
	}
	return nil
}

// xlTotalCapacity 层总承担能力 = 层限额 * (恢复次数 + 1)。
func (t Treaty) xlTotalCapacity() int64 {
	return t.XLLimit * int64(t.Reinstatements+1)
}
