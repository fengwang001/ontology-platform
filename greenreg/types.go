// Package greenreg is a green-electricity certificate registry.
//
// It supports eligibility windows per generation facility, integer-unit
// issuance with a sub-unit remainder account, wholesale transfer, retirement
// against a consumer's usage period, revocation caused by retroactive
// metering corrections (with declaration-voided events), and deterministic
// replay. See DESIGN.md for the rationale and the exported Registry methods
// for the operation API.
package greenreg

// Certificate status values.
const (
	StatusHeld = 1 + iota
	StatusRetired
	StatusRevoked
)

// Certificate is one issued green-electricity certificate.
type Certificate struct {
	Serial      int64
	Facility    string
	Generation  int64
	Holder      string
	Status      int
	RetireUser  string
	UsagePeriod int64
	RetireSeq   int64
}

// Event records an externally observable lifecycle event.
type Event struct {
	Kind        int
	Cert        int64
	Facility    string
	Generation  int64
	Holder      string
	From, To    string
	User        string
	UsagePeriod int64
}

const (
	EvIssued = 1 + iota
	EvTransferred
	EvRetired
	EvRevoked
	EvDeclarationVoided
)

// ErrCode is the machine-readable category of a rejected operation.
type ErrCode int

const (
	ErrInvalid ErrCode = iota + 1
	ErrEligibility
	ErrConflictIssued
	ErrStateNotAllowed
	ErrNotHolder
	ErrPeriodMismatch
	ErrOverUsage
	ErrBelowRetired
)

// Failure points at the first rejected certificate inside a batch.
type Failure struct {
	Cert   int64
	Reason ErrCode
}

// RegistryError carries an error category and an optional batch failure.
type RegistryError struct {
	Code    ErrCode
	Message string
	Fail    *Failure
}

func (e *RegistryError) Error() string {
	if e == nil {
		return ""
	}
	if e.Fail != nil {
		return codeName(e.Code) + ": " + e.Message + " (first failing cert=" + itoa(e.Fail.Cert) + ")"
	}
	return codeName(e.Code) + ": " + e.Message
}

func newErr(code ErrCode, msg string) *RegistryError {
	return &RegistryError{Code: code, Message: msg}
}

func failErr(code ErrCode, msg string, cert int64) *RegistryError {
	return &RegistryError{Code: code, Message: msg, Fail: &Failure{Cert: cert, Reason: code}}
}

func codeName(c ErrCode) string {
	switch c {
	case ErrInvalid:
		return "参数非法"
	case ErrEligibility:
		return "资格外"
	case ErrConflictIssued:
		return "与已核发冲突"
	case ErrStateNotAllowed:
		return "状态不允许"
	case ErrNotHolder:
		return "非持有人"
	case ErrPeriodMismatch:
		return "期限不符"
	case ErrOverUsage:
		return "超出用电量"
	case ErrBelowRetired:
		return "低于已注销量"
	default:
		return "未知错误"
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
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

// CanonicalState is a representation shared by both implementations. Each
// implementation fills it independently; renderCanonical then produces the
// exact same bytes for equal registries.
type CanonicalState struct {
	Facilities []CanonicalFacility
	Certs      []*Certificate
	Usage      []CanonicalUsage
	Events     []Event
}

// CanonicalFacility is the serializable view of one facility.
type CanonicalFacility struct {
	ID      string
	Holder  string
	Start   int64
	End     int64
	HasEnd  bool
	Balance int64
	Meters  []CanonicalMeter
}

// CanonicalMeter is the serializable view of one metered period.
type CanonicalMeter struct {
	Period int64
	Qty    int64
	Remain int64
	Live   int64
}

// CanonicalUsage is the serializable view of one user/usage-period entry.
type CanonicalUsage struct {
	User   string
	Period int64
	Qty    int64
	Units  int64
	Active int
}
