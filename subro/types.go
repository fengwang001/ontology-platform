// Package subro implements an insurance subrogation recovery
// allocation and adjustment system.
//
// After an insurer pays a claim, recoveries collected from the liable
// third party are allocated under an insured-first principle: the
// insured's uncompensated loss is made whole before the insurer
// recoups its payment, capped by the third party's liability share.
// Any net recovery beyond the cap is excess and returned to the third
// party. Every accepted operation settles the difference between
// entitled and actually paid amounts immediately via adjustment
// records.
package subro

// Party identifies one of the three stakeholders of a case.
type Party int

const (
	PartyInsured Party = iota
	PartyInsurer
	PartyThirdParty
)

func (p Party) String() string {
	switch p {
	case PartyInsured:
		return "insured"
	case PartyInsurer:
		return "insurer"
	case PartyThirdParty:
		return "third_party"
	}
	return "unknown"
}

// OpKind identifies the kind of an accepted operation.
type OpKind string

const (
	OpRegister   OpKind = "register"
	OpRecover    OpKind = "recover"
	OpAdjustRate OpKind = "adjust_ratio"
	OpSupplement OpKind = "supplement"
	OpWaive      OpKind = "waive"
)

// Entitlement holds per-party amounts, in the smallest currency unit.
type Entitlement struct {
	Insured    int64
	Insurer    int64
	ThirdParty int64
}

// Sum returns the total over all parties.
func (e Entitlement) Sum() int64 {
	return e.Insured + e.Insurer + e.ThirdParty
}

// Adjustment is the minimal settlement record emitted after an
// accepted operation for one party whose entitled amount differs
// from its already paid amount. Delta > 0 pays out more, Delta < 0
// claws back.
type Adjustment struct {
	Seq       int64
	CaseID    string
	Op        OpKind
	Party     Party
	Delta     int64
	PaidAfter int64
}

// Recovery is one registered recovery from the third party.
type Recovery struct {
	Seq     int64
	Gross   int64
	Expense int64
	Net     int64
	At      int64
}

// CaseSnapshot is a read-only view of a case's current state.
type CaseSnapshot struct {
	ID            string
	TotalLoss     int64
	InsurerPaid   int64
	Uncompensated int64
	RatioBP       int64
	Deadline      int64
	Waived        bool
	GrossTotal    int64
	ExpenseTotal  int64
	NetTotal      int64
	Cap           int64
	Entitled      Entitlement
	Paid          Entitlement
	Recoveries    int
}

// Stats exposes internal counters so tests can verify that
// settlement work stays constant per accepted operation.
type Stats struct {
	Cases            int
	AcceptedOps      int64
	Recoveries       int64
	Adjustments      int64
	SettleIterations int64
}
