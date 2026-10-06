package mileage

import "errors"

var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrClockRewind        = errors.New("clock rewind")
	ErrAccountNotFound    = errors.New("account not found")
	ErrAccountFrozen      = errors.New("account frozen")
	ErrDuplicateCredit    = errors.New("duplicate segment credit")
	ErrLateCredit         = errors.New("credit outside retroactive window")
	ErrRedemptionNotFound = errors.New("redemption not found")
	ErrRedemptionCanceled = errors.New("redemption already canceled")
	ErrInsufficientMiles  = errors.New("insufficient redeemable miles")
)

type Config struct {
	MinimumBaseMiles   int
	RetroWindowSeconds int64
	PeriodSeconds      int64
	Thresholds         [3]int
	BonusPercent       [4]int
	InactivitySeconds  int64
	CancelFeeMiles     int
}

type Segment struct {
	ID          string
	Distance    int
	FarePercent int
	FlownAt     int64
}

type CreditInput struct {
	AccountID string
	Now       int64
	Segment   Segment
}

type CreditResult struct {
	Period          int64
	LevelBefore     int
	LevelAfter      int
	BaseMiles       int
	QualifyingMiles int
	GrossRedeemable int
	DebtRepaid      int
	RedeemableAdded int
}

type RefundInput struct {
	AccountID string
	Now       int64
	SegmentID string
}

type RefundResult struct {
	Seen            bool
	Posted          bool
	Period          int64
	QualifyingMiles int
	RedeemableMiles int
	DebtCreated     int
}

type RedeemInput struct {
	AccountID    string
	RedemptionID string
	CostMiles    int
	Now          int64
}

type RedeemResult struct {
	BalanceBefore int
	BalanceAfter  int
}

type CancelRedemptionInput struct {
	AccountID    string
	RedemptionID string
	Now          int64
}

type CancelRedemptionResult struct {
	OriginallyDeducted int
	Refunded           int
	DebtRepaid         int
	AddedToBalance     int
}

type UnfreezeInput struct {
	AccountID string
	Now       int64
}

type AccountView struct {
	ID              string
	Level           int
	QualifyingMiles int
	RedeemableMiles int
	DebtMiles       int
	Frozen          bool
	CurrentPeriod   int64
	LastClockAt     int64
}

func (c Config) Validate() error {
	if c.MinimumBaseMiles < 0 || c.PeriodSeconds <= 0 || c.RetroWindowSeconds < 0 ||
		c.InactivitySeconds <= 0 || c.CancelFeeMiles < 0 {
		return ErrInvalidArgument
	}
	if c.Thresholds[0] <= 0 || c.Thresholds[1] <= c.Thresholds[0] ||
		c.Thresholds[2] <= c.Thresholds[1] {
		return ErrInvalidArgument
	}
	for _, bonus := range c.BonusPercent {
		if bonus < 0 || bonus > 1000 {
			return ErrInvalidArgument
		}
	}
	return nil
}
