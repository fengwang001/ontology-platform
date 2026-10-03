package gate

type Verdict int

const (
	Allow Verdict = iota + 1
	Inactive
	Revoked
	Expired
	Scope
	Unavailable
)

func (v Verdict) String() string {
	switch v {
	case Allow:
		return "Allow"
	case Inactive:
		return "Inactive"
	case Revoked:
		return "Revoked"
	case Expired:
		return "Expired"
	case Scope:
		return "Scope"
	case Unavailable:
		return "Unavailable"
	default:
		return "Unknown"
	}
}

type Source int

const (
	None Source = iota
	Fresh
	Cache
	Stale
)

func (s Source) String() string {
	switch s {
	case Fresh:
		return "Fresh"
	case Cache:
		return "Cache"
	case Stale:
		return "Stale"
	default:
		return "None"
	}
}

type Decision struct {
	Verdict Verdict
	Reason  string
	Source  Source
	Missing string
}
