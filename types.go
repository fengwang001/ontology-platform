package gradeaudit

import (
	"fmt"
	"sync"
)

type StudentID string
type CourseID string
type SemesterID string
type ActorID string

type RecordKey struct {
	Student  StudentID
	Course   CourseID
	Semester SemesterID
}

type Config struct {
	ReviewWindow          int64
	MinScore              int
	MaxScore              int
	MaxScoreDelta         int
	ApprovalLimit         int64
	RequiredApproverLevel int
	SpecialApproverLevel  int
	SpecialConfirmTTL     int64
	RequiredLockLevel     int
	Teachers              map[CourseID]ActorID
}

type Source int

const (
	SourceInitial Source = iota + 1
	SourceReview
	SourceSpecial
)

func (s Source) String() string {
	switch s {
	case SourceInitial:
		return "initial"
	case SourceReview:
		return "review"
	case SourceSpecial:
		return "special"
	default:
		return "unknown"
	}
}

type Version struct {
	Score  int
	At     int64
	Source Source
}

type reviewSpan struct {
	Start int64
	End   int64
}

type applicationState int

const (
	appOpen applicationState = iota + 1
	appPending
	appRejected
	appApproved
	appLocked
)

type Application struct {
	OpenedAt int64
	State    applicationState
}

type proposalState int

const (
	proposalPending proposalState = iota + 1
	proposalApproved
	proposalRejected
	proposalExpired
)

type Proposal struct {
	Teacher    ActorID
	Score      int
	CreatedAt  int64
	DeadlineAt int64
	State      proposalState
}

type specialSession struct {
	FirstActor ActorID
	Score      int
	FirstAt    int64
	ExpiresAt  int64
}

type Record struct {
	Key       RecordKey
	InitialAt int64
	Versions  []Version
	spans     []reviewSpan
	app       *Application
	proposal  *Proposal
	special   *specialSession
}

type AuditKind string

const (
	AuditInitial           AuditKind = "initial_score"
	AuditApplication       AuditKind = "review_application"
	AuditApplicationReject AuditKind = "application_rejected"
	AuditProposal          AuditKind = "change_proposed"
	AuditApproval          AuditKind = "proposal_approved"
	AuditProposalReject    AuditKind = "proposal_rejected"
	AuditProposalExpired   AuditKind = "proposal_expired"
	AuditLock              AuditKind = "semester_locked"
	AuditSpecialFirst      AuditKind = "special_first_confirmed"
	AuditSpecialSecond     AuditKind = "special_second_confirmed"
)

type AuditEntry struct {
	Seq    int64
	At     int64
	Kind   AuditKind
	Actor  ActorID
	Record RecordKey
	Score  int
	Reason string
	Detail string
}

type ErrorCode string

const (
	ErrInvalid    ErrorCode = "invalid_argument"
	ErrClock      ErrorCode = "clock_rewind"
	ErrNotFound   ErrorCode = "record_not_found"
	ErrLocked     ErrorCode = "semester_locked"
	ErrPermission ErrorCode = "permission_denied"
	ErrTimeout    ErrorCode = "window_or_deadline_closed"
	ErrState      ErrorCode = "state_not_allowed"
	ErrScore      ErrorCode = "score_out_of_bounds_or_delta"
)

type Error struct {
	Code   ErrorCode
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Reason)
}

func errorf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Reason: fmt.Sprintf(format, args...)}
}

type Engine struct {
	mu        sync.Mutex
	cfg       Config
	levels    map[ActorID]int
	clock     int64
	records   map[RecordKey]*Record
	byStudent map[studentSemesterKey]map[RecordKey]*Record
	locked    map[SemesterID]int64
	audit     []AuditEntry
	auditSeq  int64
}

type studentSemesterKey struct {
	student  StudentID
	semester SemesterID
}

type Snapshot struct {
	Record      RecordKey
	At          int64
	HasScore    bool
	Score       int
	Source      Source
	UnderReview bool
}
