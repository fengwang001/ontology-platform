package alarm

type Priority uint8

const (
	Emergency Priority = iota
	High
	Low
)

type Role uint8

const (
	Operator Role = iota
	Engineer
)

type State uint8

const (
	Normal State = iota
	ActiveUnacknowledged
	ActiveAcknowledged
	ReturnedUnacknowledged
)

type ErrorKind uint8

const (
	InvalidArgument ErrorKind = iota
	ClockRewound
	PointNotFound
	PermissionDenied
	StateNotAllowed
	DurationLimitExceeded
)

type PointConfig struct {
	ID         string
	Priority   Priority
	Conditions []string
}

type Config struct {
	HighManualDuration int64
	LowManualDuration  int64
	ChatterWindow      int64
	ChatterCount       int
	ChatterDuration    int64
}

type Operation struct {
	At       int64
	PointID  string
	Role     Role
	Duration int64
	Reason   string
	Ticket   string
}

type Result struct {
	Ignored bool
	Visible bool
	State   State
}

type ActiveAlarm struct {
	PointID        string
	Priority       Priority
	State          State
	LastActivation int64
}

type ConditionUpdate struct {
	At        int64
	Condition string
	Active    bool
}
