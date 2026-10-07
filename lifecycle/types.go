package lifecycle

import (
	"errors"
	"time"
)

// State 是对象的逻辑删除状态，四态互斥。
type State string

const (
	// StateAlive 存活。
	StateAlive State = "alive"
	// StateGrace 待撤销宽限。
	StateGrace State = "grace"
	// StateArchived 已归档（终态）。
	StateArchived State = "archived"
	// StateFrozen 保留期冻结。
	StateFrozen State = "frozen"
)

// Identity 是查询身份。
type Identity string

const (
	// IdentityAdmin 数据管理员：可见全部四种状态。
	IdentityAdmin Identity = "admin"
	// IdentityUser 一般使用者：仅可见存活对象。
	IdentityUser Identity = "user"
)

// 四类互斥错误，按 ErrObjectNotFound → ErrInvalidTransition →
// ErrInvalidTime → ErrFrozenNotExpired 的固定次序判定，只报第一类。
var (
	ErrObjectNotFound    = errors.New("lifecycle: object not found")
	ErrInvalidTransition = errors.New("lifecycle: state transition direction not allowed")
	ErrInvalidTime       = errors.New("lifecycle: illegal grace deadline or freeze duration")
	ErrFrozenNotExpired  = errors.New("lifecycle: frozen retention period has not expired")
)

// Attrs 是对象的业务属性取值。
type Attrs map[string]string

// Edge 是对象的一条出边链接。链接自身不携带任何删除状态，
// 其可见性完全跟随源对象。
type Edge struct {
	ID       string
	SourceID string
	TargetID string
}

// Transition 是一条不可变的状态转换历史记录（审计）。
type Transition struct {
	Seq      int
	ObjectID string
	Time     time.Time
	From     State
	To       State
	Kind     string // "delete" | "undo" | "freeze" | "archive" | "auto-archive"
	Deadline time.Time
	Actor    Identity
}

// View 是一次对象可见性查询的结果。
type View struct {
	ID             string
	Exists         bool
	Visible        bool
	State          State
	FreezeDeadline time.Time
	GraceDeadline  time.Time
	Attrs          Attrs
}

// LogEntry 记录一次操作的输入、输出与据以判定的状态和时刻。
type LogEntry struct {
	Time        time.Time
	Operation   string
	ObjectID    string
	Actor       Identity
	Input       string
	StateBefore State
	StateAfter  State
	Output      string
	Err         string
}
