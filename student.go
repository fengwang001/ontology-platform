package enrollment

type Status int

const (
	StatusEnrolled Status = iota
	StatusSuspended
	StatusReserved
	StatusWithdrawn
	StatusGraduated
)

func (st Status) terminal() bool { return st == StatusWithdrawn || st == StatusGraduated }

func (st Status) String() string {
	switch st {
	case StatusEnrolled:
		return "enrolled"
	case StatusSuspended:
		return "suspended"
	case StatusReserved:
		return "reserved"
	case StatusWithdrawn:
		return "withdrawn"
	case StatusGraduated:
		return "graduated"
	default:
		return "invalid"
	}
}

type AppType int

const (
	AppSuspend AppType = iota
	AppResume
	AppTransfer
	AppReserve
	AppWithdraw
)

func (t AppType) String() string {
	switch t {
	case AppSuspend:
		return "suspend"
	case AppResume:
		return "resume"
	case AppTransfer:
		return "transfer"
	case AppReserve:
		return "reserve"
	case AppWithdraw:
		return "withdraw"
	default:
		return "invalid"
	}
}

// resultStatus is the state the student enters when the application is approved.
func (t AppType) resultStatus() (Status, bool) {
	switch t {
	case AppSuspend:
		return StatusSuspended, true
	case AppResume:
		return StatusEnrolled, true
	case AppTransfer:
		return StatusEnrolled, true
	case AppReserve:
		return StatusReserved, true
	case AppWithdraw:
		return StatusWithdrawn, true
	default:
		return 0, false
	}
}

// allowedFrom reports whether an application of this type may be submitted
// while the student currently shows the given status.
func (t AppType) allowedFrom(st Status) bool {
	switch t {
	case AppSuspend, AppTransfer, AppReserve, AppWithdraw:
		return st == StatusEnrolled
	case AppResume:
		return st == StatusSuspended || st == StatusReserved
	default:
		return false
	}
}

type Version struct {
	EffectiveAt int64
	Status      Status
	Major       string
	// LeaveTerms is the declared length for a suspend/reserve version.
	LeaveTerms int
	// AcceptedAt is the tick of the final approval; a version approved after
	// a query tick must not be visible even if its effective term started.
	AcceptedAt int64
}

type application struct {
	id            string
	typ           AppType
	submittedAt   int64
	submitter     string
	targetMajor   string
	terms         int
	levels        int
	level         int
	approvers     []string
	closed        bool
	accepted      bool
	rejected      bool
	expired       bool
	effectiveTerm int
	closedAt      int64
}

type student struct {
	id        string
	entryTerm int
	major     string
	versions  []Version
	app       *application
	appHist   []*application
	// yearsExhausted is set by a rejected resume (years used up); the actual
	// withdrawal is lazily landed on the next mutating touch.
	yearsExhausted bool
}

func (s *student) headVersion() Version { return s.versions[len(s.versions)-1] }

// stateAt returns the version effective at tick. A version starting exactly at
// tick is active; when several versions share the tick (the admission baseline
// and the first change) the latest written one wins. O(log v).
func (s *student) stateAt(tick int64) Version {
	vs := s.versions
	if len(vs) == 0 || tick < vs[0].EffectiveAt {
		return Version{}
	}
	var out Version
	for _, v := range vs {
		if v.EffectiveAt <= tick && (v.AcceptedAt == 0 || v.AcceptedAt <= tick) {
			out = v
		}
	}
	return out
}

// openAt reports whether an application existed and was unresolved at tick.
func (a *application) openAt(tick int64) bool {
	return a.submittedAt <= tick && (!a.closed || a.closedAt > tick)
}
