package reinsurance

import "errors"

var (
	ErrInvalidArgument  = errors.New("参数非法")
	ErrPolicyNotFound   = errors.New("保单不存在")
	ErrPolicyDuplicate  = errors.New("保单重复")
	ErrCapacityExceeded = errors.New("超出承保能力")
	ErrClaimExists      = errors.New("赔款已存在")
	ErrClaimNotFound    = errors.New("赔款不存在")
	ErrNotCovered       = errors.New("事故未承保")
)

type Terms struct {
	QuotaPercent     int
	SurplusLine      int64
	SurplusLines     int
	XLDeductible     int64
	XLLimit          int64
	XLReinstatements int
}

type Policy struct {
	ID           string
	Limit        int64
	StartDay     int64
	EndDay       int64
	QuotaShare   int64
	SurplusShare int64
	NetRetention int64
}

type Claim struct {
	ID          string
	PolicyID    string
	Occurrence  string
	TimeSec     int64
	Amount      int64
	QuotaPart   int64
	SurplusPart int64
	NetPart     int64
}

type OccurrenceResult struct {
	Occurrence   string
	TimeSec      int64
	NetAggregate int64
	XLRecovery   int64
}

type Totals struct {
	Claims      int64
	Gross       int64
	Quota       int64
	Surplus     int64
	Net         int64
	XL          int64
	NetAfterXL  int64
	XLRemaining int64
}
