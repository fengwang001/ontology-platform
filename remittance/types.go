package remittance

const (
	secondsPerDay = 86400
	annualDays    = 365
	rateScale     = 1_000_000
)

// Limits 是汇款人的三项限额，单位均为目标币种最小单位。
type Limits struct {
	Single int64
	Daily  int64
	Annual int64
}

// QuoteRequest 是报价申请入参。
type QuoteRequest struct {
	Sender    string
	SourceCCY string
	TargetCCY string
	Amount    int64 // 源币种最小单位
	RatePPM   int64 // 每个源最小单位折合目标最小单位的百万分之几（正整数）
	Now       int64
}

// SubmitRequest 是汇款提交入参。
type SubmitRequest struct {
	Sender  string
	QuoteID int64
	Payee   string
	IdemKey string
	Now     int64
}

// TransferStatus 是汇款生命周期状态。
type TransferStatus int

const (
	// StatusPending 待人工审核（额度已占用、未出款）。
	StatusPending TransferStatus = iota + 1
	// StatusSucceeded 已成功出款（提交即成功，或审核批准）。
	StatusSucceeded
	// StatusFailed 失败（审核拒绝、审核逾期或撤回），额度已释放。
	StatusFailed
)

func (s TransferStatus) String() string {
	switch s {
	case StatusPending:
		return "PENDING"
	case StatusSucceeded:
		return "SUCCESS"
	case StatusFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// SubmitResult 是提交/幂等重放的返回。
type SubmitResult struct {
	TransferID     int64
	TargetAmount   int64 // 目标额：floor(amount*rate/1e6)，即收款人实际入账额
	Occupied       int64 // 占用额：ceil(amount*rate/1e6)
	Status         TransferStatus
	ReviewDeadline int64 // 仅 Pending 有意义；提交时刻 + R
	Replay         bool  // 是否为幂等重放（原结果原样返回）
}

// TransferInfo 是单笔汇款的快照。
type TransferInfo struct {
	ID             int64
	Sender         string
	Payee          string
	QuoteID        int64
	SourceAmount   int64
	RatePPM        int64
	TargetAmount   int64
	Occupied       int64
	Day            int64 // 提交所属自然日序号
	Status         TransferStatus
	SubmittedAt    int64
	ReviewDeadline int64
	DecidedAt      int64 // 成功出款或失败（释放）生效时刻；0 表示尚未决定
	IdemKey        string
}

// Usage 是某汇款人在给定时刻的占用快照。
type Usage struct {
	Day        int64
	DayUsed    int64 // 该自然日内仍生效的占用合计
	AnnualUsed int64 // 滚动年度窗口内仍生效的占用合计（含 Day）
}
