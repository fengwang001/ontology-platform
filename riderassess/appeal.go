package riderassess

// Appeal 是一条申诉记录。
type Appeal struct {
	ID           int64
	EventID      int64
	Rider        string
	FiledAt      int64
	Ruled        bool
	Upheld       bool
	RuledAt      int64
	Compensation int
}

// Compensation 是补偿账目明细。
type Compensation struct {
	AppealID int64
	Rider    string
	Period   int64
	Amount   int
}
