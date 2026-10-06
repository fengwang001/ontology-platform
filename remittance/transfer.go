package remittance

// transfer 是单笔汇款的完整内部记录。
type transfer struct {
	id             int64
	sender         string
	payee          string
	quoteID        int64
	sourceAmount   int64
	ratePPM        int64
	targetAmount   int64
	occupied       int64
	day            int64
	status         TransferStatus
	submittedAt    int64
	reviewDeadline int64
	decidedAt      int64
	idemKey        string
}

func (t *transfer) snapshot() TransferInfo {
	return TransferInfo{
		ID:             t.id,
		Sender:         t.sender,
		Payee:          t.payee,
		QuoteID:        t.quoteID,
		SourceAmount:   t.sourceAmount,
		RatePPM:        t.ratePPM,
		TargetAmount:   t.targetAmount,
		Occupied:       t.occupied,
		Day:            t.day,
		Status:         t.status,
		SubmittedAt:    t.submittedAt,
		ReviewDeadline: t.reviewDeadline,
		DecidedAt:      t.decidedAt,
		IdemKey:        t.idemKey,
	}
}
