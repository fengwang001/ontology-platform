package settlement

// Day is an abstract business-calendar coordinate: any integer (for example a
// number of days since an epoch). Only membership in the business-day set makes
// a day a business day; transaction dates themselves need not be business days.
type Day int64

// Amount is a signed integer amount in the system's minor currency unit.
type Amount int64

// MerchantConfig describes a merchant's settlement and reserve policy.
//
//   - SettleDelayN is the settlement delay in business days (>= 1): on business
//     day t, transactions whose date is no later than the N-th business day on
//     or before t become settleable.
//   - ReserveBps is the rolling reserve fraction in basis points (0..10000).
//   - ReserveHorizonH is the reserve lifetime in business days (>= 1): a batch
//     retained on business day t is released on the H-th business day strictly
//     after t.
type MerchantConfig struct {
	SettleDelayN    int
	ReserveBps      int
	ReserveHorizonH int
}

// Transaction is a single signed ledger entry for a merchant. Payments are
// positive; refunds, chargebacks and fees are negative.
type Transaction struct {
	ID     string
	Day    Day
	Amount Amount
}

// Payout is the one-record-per-settled-business-day output.
type Payout struct {
	MerchantID string
	Day        Day
	Amount     Amount
}

// ReserveBatch is one retained reserve batch. Consumed amounts are permanently
// spent covering negative net balances and are never released.
type ReserveBatch struct {
	RetainDay  Day
	ReleaseDay Day
	Amount     Amount
	Consumed   Amount
}

// Available returns the not-yet-consumed balance of the batch.
func (b ReserveBatch) Available() Amount { return b.Amount - b.Consumed }

// MerchantState is an immutable snapshot of one merchant.
type MerchantState struct {
	ID             string
	Config         MerchantConfig
	LastSettledDay Day // -inf sentinel when no business day settled yet
	Settled        bool
	Payouts        []Payout
	Batches        []ReserveBatch // active, not yet (fully) released/consumed
	ReserveBalance Amount
	NegativeCarry  Amount // <= 0
	TotalPayout    Amount
}
