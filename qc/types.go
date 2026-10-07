package qc

// RuleID 质控规则编号，取值 1..5，判定结果按此升序列出。
type RuleID int

const (
	// Rule1 任一水平单点超过 3 倍标准差。
	Rule1 RuleID = 1
	// Rule2 同一水平连续两点同侧且都超过 2 倍标准差。
	Rule2 RuleID = 2
	// Rule3 本次运行一水平正向超 2 倍标准差、另一水平负向超 2 倍标准差。
	Rule3 RuleID = 3
	// Rule4 同一水平连续四点同侧且都超过 1 倍标准差。
	Rule4 RuleID = 4
	// Rule5 同一水平连续十点都在同一侧。
	Rule5 RuleID = 5
)

// RunStatus 单次运行的判定结论。
type RunStatus int

const (
	// RunNormal 未触发任何规则，且两个水平单点都不超过 2 倍标准差。
	RunNormal RunStatus = iota
	// RunWarning 未触发任何规则，但存在任一水平单点超过 2 倍标准差。
	RunWarning
	// RunRejected 触发至少一条规则，该次运行失控。
	RunRejected
)

// ProjectState 项目状态。
type ProjectState int

const (
	// StateInControl 在控。
	StateInControl ProjectState = iota
	// StateOutOfControl 失控，暂停报告出具。
	StateOutOfControl
)

// ReportStatus 报告状态。
type ReportStatus int

const (
	// ReportIssued 已出具（正常状态）。
	ReportIssued ReportStatus = iota
	// ReportPendingReview 失控追溯后待复核。
	ReportPendingReview
	// ReportReviewed 复核完成。
	ReportReviewed
)

// RunResult 一次质控运行的判定输出。
type RunResult struct {
	Rules  []RuleID  // 全部被触发的规则，按 1..5 升序；为空表示未触发任何规则
	Status RunStatus // 失控 / 警告 / 正常
}

// Report 患者报告记录。
type Report struct {
	ID      int64
	Analyte string
	Time    int64
	Status  ReportStatus
}
