package ledger

// BatchLine 记录一次领用中某个批次分出的数量。
type BatchLine struct {
	BatchID string
	Qty     int
}

// SlipStatus 是单据的生命周期状态。
type SlipStatus int

const (
	SlipOpen        SlipStatus = iota // 已领用，尚未提交结清
	SlipSettled                       // 已结清（含差额处理完成）
	SlipDiscrepancy                   // 差额待处理
)

func (s SlipStatus) String() string {
	switch s {
	case SlipOpen:
		return "未结清"
	case SlipSettled:
		return "已结清"
	case SlipDiscrepancy:
		return "差额待处理"
	}
	return "未知"
}

// 结清期限：领用时刻起 24 小时，恰到期时刻仍属期内。
const settleWindow = int64(24 * 3600)

// slip 是一张领用单据。
type slip struct {
	id        string
	dept      string
	applicant string
	drug      string
	lines     []BatchLine
	qty       int
	issueNow  int64
	deadline  int64 // issueNow + settleWindow；now > deadline 视为逾期
	status    SlipStatus
	used      int
	returned  int
	residual  int
	diff      int // 差额待处理时记下的差额
	heapIndex int // 在所属科室逾期堆中的下标
}

func (s *slip) overdueAt(now int64) bool {
	return s.status == SlipOpen && now > s.deadline
}
