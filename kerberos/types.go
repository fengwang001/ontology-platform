package kerberos

// Krbtgt is the fixed service name of a ticket granting ticket.
const Krbtgt = "krbtgt"

// Reasons returned by failed operations.
const (
	ReasonConfigInvalid   = "config_invalid"
	ReasonInvalidParam    = "invalid_param"
	ReasonClockRewind     = "clock_rewind"
	ReasonTicketNotFound  = "ticket_not_found"
	ReasonNotTGT          = "not_a_tgt"
	ReasonNotYetValid     = "not_yet_valid"
	ReasonInvalidFlag     = "invalid_flag"
	ReasonExpired         = "expired"
	ReasonKeyChanged      = "key_changed"
	ReasonBadInterval     = "bad_interval"
	ReasonPostdatedTooFar = "postdated_too_far"
	ReasonNotRenewable    = "not_renewable"
	ReasonRenewLimit      = "renew_limit"
	ReasonNotPostdated    = "not_postdated"
	ReasonClockSkew       = "clock_skew"
	ReasonReplay          = "replay"
)

// Op tags identifying which operation produced an error.
const (
	OpNewKDC       = "NewKDC"
	OpIssueTGT     = "IssueTGT"
	OpTGS          = "TGS"
	OpRenew        = "Renew"
	OpValidate     = "Validate"
	OpAuthenticate = "Authenticate"
	OpChangeKey    = "ChangeKey"
)

// Error is a distinguishable rejection carrying the operation, the reason of
// the first failed check, and the ticket id when a ticket was located.
type Error struct {
	Op       string
	Reason   string
	Message  string
	TicketID int64
}

func (e *Error) Error() string {
	if e.TicketID > 0 {
		return e.Op + ": " + e.Reason + " (ticket t" + itoa(e.TicketID) + "): " + e.Message
	}
	return e.Op + ": " + e.Reason + ": " + e.Message
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Ticket is a TGT or a service ticket.
type Ticket struct {
	ID        int64
	Subject   []byte
	Service   []byte
	IsTGT     bool
	Start     int64
	End       int64
	RenewTill int64
	Invalid   bool
	Issued    int64
}

func newError(op, reason, msg string) *Error {
	return &Error{Op: op, Reason: reason, Message: msg}
}

func withTicket(e *Error, id int64) *Error {
	e.TicketID = id
	return e
}
