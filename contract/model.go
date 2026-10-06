package contract

// 固定的自动续签相关条款编号。
const (
	ClauseAutoRenew   = "AUTO_RENEW"   // 是否自动续签（1=是，0=否）
	ClauseRenewalTerm = "RENEWAL_TERM" // 续签一期的长度（天，>0）
	ClauseNoticeDays  = "NOTICE_DAYS"  // 不续签通知需提前的天数（>=0）
)

// PartyInput 合同一方及其签署授权有效期（含两端）。
type PartyInput struct {
	ID        string
	AuthFrom  int64
	AuthUntil int64
}

// CreateContractInput 创建主合同的参数。
type CreateContractInput struct {
	ID            string
	Parties       [2]PartyInput
	Clauses       map[string]int
	LockedClauses map[string]bool
	StartDay      int64
	ExpiryDay     int64
	Now           int64
}

// AddAmendmentInput 创建补充协议（条款修改或撤销协议）。
// Revokes 非空表示撤销协议；否则 Changes 必须非空。
type AddAmendmentInput struct {
	ContractID   string
	AmendmentID  string
	Now          int64
	EffectiveDay int64 // 期望生效日；不得早于签署完成日，否则后移
	Changes      map[string]int
	Revokes      string
}

// ValueSource 描述某日某条款有效值的来源。
type ValueSource struct {
	Value int
	// Kind = "MAIN" 表示来自主合同；= "AMENDMENT" 表示来自补充协议。
	Kind         string
	AmendmentID  string
	EffectiveDay int64
}

// RenewalRecord 一次续签的记录。
type RenewalRecord struct {
	FromExpiryDay int64 // 触发续签的原到期日
	NewExpiryDay  int64 // 续签后的新到期日（= FromExpiryDay + 期长）
	TermLength    int   // 本次续签使用的期长（取原到期日当日有效值）
}

// OperationLog 一步操作的输入/输出/判定依据日志条目。
type OperationLog struct {
	Seq    int
	Op     string
	Input  string
	Output string
	Reason string
}
