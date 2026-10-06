package contacttracing

// Stay 是一条不可变的住宿区间，左闭右开 [CheckIn, CheckOut)。
// CheckOutOpen 为 true 时 CheckOut 视为尚未登记出住，区间持续到 now。
type Stay struct {
	Patient      string
	Room         string
	CheckIn      int64
	CheckOut     int64
	CheckOutOpen bool
}

// Case 是一名患者的病例登记。
type Case struct {
	ID            string
	Patient       string
	OnsetAt       int64
	RegisteredAt  int64
	IsolatedAt    int64
	IsolationOpen bool
	Revoked       bool
}

// ContactKind 表示接触等级。
type ContactKind int

const (
	KindClose     ContactKind = iota + 1 // 密切接触者
	KindSecondary                        // 次密接
)

func (k ContactKind) String() string {
	if k == KindClose {
		return "CLOSE"
	}
	return "SECONDARY"
}

// PatientStatus 是状态查询结果。
type PatientStatus int

const (
	StatusUnrelated PatientStatus = iota
	StatusCloseQuarantine
	StatusSecondaryObservation
	StatusReleased
)

func (s PatientStatus) String() string {
	switch s {
	case StatusCloseQuarantine:
		return "CLOSE_QUARANTINE"
	case StatusSecondaryObservation:
		return "SECONDARY_OBSERVATION"
	case StatusReleased:
		return "RELEASED"
	default:
		return "UNRELATED"
	}
}

const (
	// MinContactMinutes 是密接/次密接的累计时长阈值（含等于）。
	MinContactMinutes int64 = 120
	// InfectiousLeadMinutes 是传染期相对发病时刻向前延伸的时长。
	InfectiousLeadMinutes int64 = 48 * 60
	// CloseQuarantineMinutes 是密接自最后接触时刻起的隔离时长 7 天。
	CloseQuarantineMinutes int64 = 7 * 24 * 60
	// SecondaryObservationMinutes 是次密接自最后接触时刻起的观察时长 3 天。
	SecondaryObservationMinutes int64 = 3 * 24 * 60
)
