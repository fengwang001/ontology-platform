package ontology

// ErrCode identifies the distinct, mutually exclusive error categories the
// view maintenance subsystem can report.
type ErrCode int

const (
	// ErrLinkEndpointMissing: either endpoint object type of the link
	// relation the view aggregates over no longer exists.
	ErrLinkEndpointMissing ErrCode = iota + 1
	// ErrNoTimezoneAtWrite: the object's type had no default timezone
	// defined at the moment the time property value was written.
	ErrNoTimezoneAtWrite
	// ErrMigrationInvalid: the timezone definition version anchored by the
	// write is not an accepted version (its migration failed validation).
	ErrMigrationInvalid
	// ErrPropertyDeprecated: the time property the view groups by was
	// deprecated by an object type version migration.
	ErrPropertyDeprecated
)

// errPriority orders error categories for reporting: when several categories
// are triggered in the same maintenance pass, only the one with the smallest
// priority value is reported.
var errPriority = map[ErrCode]int{
	ErrLinkEndpointMissing: 1,
	ErrNoTimezoneAtWrite:   2,
	ErrMigrationInvalid:    3,
	ErrPropertyDeprecated:  4,
}

func (c ErrCode) String() string {
	switch c {
	case ErrLinkEndpointMissing:
		return "link endpoint object type missing"
	case ErrNoTimezoneAtWrite:
		return "no default timezone defined at write time"
	case ErrMigrationInvalid:
		return "timezone definition version migration validation failed"
	case ErrPropertyDeprecated:
		return "grouping time property deprecated by type migration"
	}
	return "unknown error"
}

// ViewError is the single error category reported by one maintenance pass.
type ViewError struct {
	Code    ErrCode
	Detail  string
	Objects []string // affected object ids, quarantined from the view
}

func (e *ViewError) Error() string { return e.Code.String() + ": " + e.Detail }

// Report summarizes one incremental maintenance pass.
type Report struct {
	Applied int        // events applied in this pass
	Err     *ViewError // highest-priority error category, if any
}
