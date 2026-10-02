package kerberos

import "fmt"

type Error struct {
	Operation string
	Reason    string
	TicketID  string
	Detail    string
}

func (e *Error) Error() string {
	if e.TicketID != "" {
		return fmt.Sprintf("%s %s: %s", e.Operation, e.TicketID, e.Reason)
	}
	return e.Operation + ": " + e.Reason
}

const (
	ErrInvalidConfig    = "invalid config"
	ErrInvalidParameter = "invalid parameter"
	ErrClockRollback    = "clock rollback"
	ErrTicketNotFound   = "ticket not found"
	ErrNotTGT           = "not a tgt"
	ErrInvalidInterval  = "invalid interval"
	ErrTooFarPostdated  = "postdated too far"
	ErrNotYetValid      = "not yet valid"
	ErrInvalidTicket    = "invalid ticket"
	ErrExpired          = "expired"
	ErrKeyChanged       = "key changed"
	ErrNotRenewable     = "not renewable"
	ErrRenewLimit       = "renewal limit"
	ErrNotPostdated     = "not postdated"
	ErrClockSkew        = "clock skew"
	ErrReplay           = "replay"
)
