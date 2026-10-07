package permission

import "time"

type Time = time.Time

type Decision string

const (
	Deny  Decision = "DENY"
	Allow Decision = "ALLOW"
)

type Kind string

const (
	Put    Kind = "PUT"
	Revoke Kind = "REVOKE"
)

type Change struct {
	ID          string
	Subject     string
	Label       string
	Kind        Kind
	Decision    Decision
	RevokesID   string
	EffectiveAt time.Time
	SubmittedAt time.Time
}

type SubmitResult struct {
	Accepted bool
	OrderKey string
}

type WithdrawRequest struct {
	ID          string
	WithdrawnAt time.Time
}

type Query struct {
	Subject   string
	Label     string
	At        time.Time
	ObserveID bool
}

type QueryResult struct {
	Decision         Decision
	WinningChangeID  string
	ExaminedChangeID []string
}

type AuditRecord struct {
	Call          string
	Input         string
	Output        string
	TemporalBasis string
}
