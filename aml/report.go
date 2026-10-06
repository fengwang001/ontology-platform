package aml

// ReportKind 区分报告类型。
type ReportKind int

const (
	// KindLarge 为大额报告：单笔金额不小于 H 时立即产生。
	KindLarge ReportKind = iota
	// KindStructuring 为结构化报告：客户组窗口内小额存款集合达到阈值时产生。
	KindStructuring
)

func (k ReportKind) String() string {
	if k == KindLarge {
		return "LARGE"
	}
	return "STRUCTURING"
}

// Report 为一份已发出的报告，发出后不可变、不撤回。
type Report struct {
	ID       int        // 全局编号，按产生次序从 1 开始
	Kind     ReportKind // 报告类型
	Accounts []string   // 触发时刻客户组包含的全部账户（排序，确定性）
	TxnIDs   []string   // 报告覆盖的相关交易号（排序，确定性）
	Total    int64      // 集合内交易号金额合计
	Trigger  string     // 触发该报告的操作描述
	Date     int64      // 触发时刻的 now
}
