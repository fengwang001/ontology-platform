// Package alloc 计算代位追偿回收款的应得分配。
//
// 本包是纯函数模块：应得只取决于当前净回收总额、可追偿上限、
// 被保险人未获赔额、保险人已赔付额与放弃标记，与回收发生的先后无关。
package alloc

// Entitlements 三方各自应得金额。
type Entitlements struct {
	Insured    int64 // 被保险人
	Insurer    int64 // 保险人
	ThirdParty int64 // 超额部分，应退还第三方
}

// Allocation 一次分配计算的完整结果，含中间量（可作为判定依据输出）。
type Allocation struct {
	Cap           int64 // 可追偿上限
	Distributable int64 // 参与分配的净回收额 = min(净回收总额, Cap)
	Entitlements
}

// Cap 可追偿上限 = floor(totalLoss * ratioBP / 10000)。
// 拆分计算避免 totalLoss*ratioBP 中间积溢出 int64。
func Cap(totalLoss, ratioBP int64) int64 {
	q := totalLoss / 10000
	r := totalLoss % 10000
	return q*ratioBP + r*ratioBP/10000
}

// Compute 按分配瀑布计算应得：
// 净回收总额超过上限的部分直接属超额退还第三方；
// 其余先补足被保险人未获赔额（已放弃则视为零），再归保险人至其已赔付额为止，
// 仍有剩余的亦属超额退还第三方。
func Compute(netTotal, cap, uncompensated, insurerPaid int64, waived bool) Allocation {
	distributable := min(netTotal, cap)
	var insured int64
	if !waived {
		insured = min(distributable, uncompensated)
	}
	insurer := min(distributable-insured, insurerPaid)
	return Allocation{
		Cap:           cap,
		Distributable: distributable,
		Entitlements: Entitlements{
			Insured:    insured,
			Insurer:    insurer,
			ThirdParty: netTotal - insured - insurer,
		},
	}
}
