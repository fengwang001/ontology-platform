package hospital

type ErrorCode string

const (
	ErrInvalidArgument ErrorCode = "参数非法"
	ErrClockRollback   ErrorCode = "时钟回退"
	ErrNotFound        ErrorCode = "对象不存在"
	ErrRecallBlocked   ErrorCode = "召回禁止"
	ErrConsentRequired ErrorCode = "需知情确认"
	ErrInsufficient    ErrorCode = "库存不足"
	ErrReturnExceeded  ErrorCode = "退药超量"
	ErrInvalidState    ErrorCode = "状态不符"
)

const Warehouse = "药库"

type InboundInput struct {
	Now   int64
	Drug  string
	Batch string
	Qty   int
}

type TransferInput struct {
	Now   int64
	Drug  string
	Batch string
	From  string
	To    string
	Qty   int
}

type DispenseInput struct {
	Now             int64
	Drug            string
	Batch           string
	Location        string
	Patient         string
	Qty             int
	InformedConsent bool
}

type ReturnInput struct {
	Now     int64
	Drug    string
	Batch   string
	Patient string
	Qty     int
}

type RecallInput struct {
	Now     int64
	ID      string
	Drug    string
	First   string
	Last    string
	Level   int
	IssueAt int64
}

type CancelRecallInput struct {
	Now int64
	ID  string
}

type RecoveryInput struct {
	Now int64
	ID  string
}

type BatchQueryInput struct {
	Now   int64
	Drug  string
	Batch string
}

type PositionStock struct {
	Position string
	Qty      int
}

type RecoveryItem struct {
	Patient     string
	Batch       string
	Outstanding int
}

type BatchView struct {
	Drug      string
	Batch     string
	Stocks    []PositionStock
	Level     int
	RecallIDs []string
}

type OperationError struct {
	Code   ErrorCode
	Reason string
}

func (e *OperationError) Error() string {
	return string(e.Code) + ": " + e.Reason
}
