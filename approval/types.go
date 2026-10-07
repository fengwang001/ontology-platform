package approval

type StageDefinition struct {
	ID              string
	Department      string
	TimeLimit       int
	Prerequisites   []string
	AutoPassOnTime  bool
	CorrectionLimit int
	CorrectionDays  int
}

type LicenseType struct {
	ID     string
	Stages []StageDefinition
}

type AcceptRequest struct {
	LicenseID   string
	TypeID      string
	ApplicantID string
	Actors      map[string]Actor
	Day         int
}

type StageCommand struct {
	LicenseID string
	StageID   string
	Actor     string
	Day       int
}

type WithdrawRequest struct {
	LicenseID string
	Actor     string
	Day       int
}

type Actor struct {
	ID         string
	Department string
}

const (
	StageNotStarted = "未启动"
	StageProcessing = "办理中"
	StageCorrecting = "补正中"
	StagePassed     = "已通过"
	StageFailed     = "不通过"
	StageWithdrawn  = "已撤回"
	StageTerminated = "因他环节终止而未启动"
)

const (
	OverallAccepted   = "已受理"
	OverallProcessing = "办理中"
	OverallGranted    = "准予许可"
	OverallDenied     = "不予许可"
	OverallWithdrawn  = "已撤回"
)

type StageView struct {
	ID                 string
	Department         string
	Status             string
	RemainingWorkdays  *int
	Overdue            bool
	StartedAt          *int
	PassedAt           *int
	FailedAt           *int
	CorrectionDeadline *int
}

type Progress struct {
	LicenseID string
	Status    string
	FinalAt   *int
	Stages    map[string]StageView
}
