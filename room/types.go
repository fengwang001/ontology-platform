// Package room 实现多人对局房间的生命周期服务。
//
// 房间状态机：等待 -> 倒计时 -> 进行中 -> 结算中 -> 已结束 / 已作废。
// 所有时间均为调用方传入的逻辑时钟 now（惰性到期，不用真实定时器），
// 因此任意操作序列都可以在测试中精确复现。
package room

import "fmt"

// MaxNow 是 now 的合法上界（含）。
const MaxNow = int64(1_000_000_000_000)

// State 为房间生命周期状态。
type State int

const (
	StateWaiting    State = iota // 等待
	StateCountdown               // 倒计时
	StateInProgress              // 进行中
	StateSettling                // 结算中
	StateEnded                   // 已结束（终态）
	StateAborted                 // 已作废（终态）
)

func (s State) String() string {
	switch s {
	case StateWaiting:
		return "waiting"
	case StateCountdown:
		return "countdown"
	case StateInProgress:
		return "in_progress"
	case StateSettling:
		return "settling"
	case StateEnded:
		return "ended"
	case StateAborted:
		return "aborted"
	}
	return "unknown"
}

// Terminal 报告状态是否为终态。
func (s State) Terminal() bool { return s == StateEnded || s == StateAborted }

// ErrCode 为拒绝原因编码。一次拒绝只报告按优先级排第一的原因。
//
// 优先级（高 -> 低）：
// 参数非法 > 时钟回退 > 阶段不允许（含已终止）> 玩家不在室/不在对局名单 >
// 权限不足 > 状态冲突（已就绪再就绪 / 已满 / 已加入）。
type ErrCode int

const (
	ErrCodeNone ErrCode = iota
	ErrCodeInvalidParam
	ErrCodeClockRollback
	ErrCodePhaseNotAllowed
	ErrCodeNotInRoom
	ErrCodeNotHost
	ErrCodeAlreadyReady
	ErrCodeRoomFull
	ErrCodeAlreadyJoined
	ErrCodeTerminated
)

func (c ErrCode) String() string {
	switch c {
	case ErrCodeNone:
		return "ok"
	case ErrCodeInvalidParam:
		return "invalid_param"
	case ErrCodeClockRollback:
		return "clock_rollback"
	case ErrCodePhaseNotAllowed:
		return "phase_not_allowed"
	case ErrCodeNotInRoom:
		return "not_in_room_or_match"
	case ErrCodeNotHost:
		return "not_host"
	case ErrCodeAlreadyReady:
		return "already_ready"
	case ErrCodeRoomFull:
		return "room_full"
	case ErrCodeAlreadyJoined:
		return "already_joined"
	case ErrCodeTerminated:
		return "terminated"
	}
	return "unknown"
}

// Error 为操作被拒绝时返回的错误。
type Error struct {
	Code ErrCode
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("room: %s rejected (%s): %s", e.Op, e.Code, e.Msg)
}

// AbortReason 为房间作废原因。
type AbortReason int

const (
	AbortNone         AbortReason = iota
	AbortEmpty                    // 等待/倒计时阶段无人在室
	AbortInsufficient             // 进行中在室对局玩家不足两人
	AbortDispute                  // 全体上报但不一致（争议）
	AbortTimeout                  // 上报期限到期且未满足多数一致
)

func (r AbortReason) String() string {
	switch r {
	case AbortNone:
		return "none"
	case AbortEmpty:
		return "empty"
	case AbortInsufficient:
		return "insufficient_players"
	case AbortDispute:
		return "dispute"
	case AbortTimeout:
		return "timeout"
	}
	return "unknown"
}

// Config 为房间创建参数。
type Config struct {
	MinPlayers   int64 // L：人数下限，2..20
	MaxPlayers   int64 // U：人数上限，L..20
	Countdown    int64 // C：倒计时时长（秒），1..600
	ReportWindow int64 // R：结果上报期限（秒），1..3600
}

func (c Config) validate() error {
	if c.MinPlayers < 2 || c.MinPlayers > 20 {
		return fmt.Errorf("MinPlayers %d out of [2,20]", c.MinPlayers)
	}
	if c.MaxPlayers < c.MinPlayers || c.MaxPlayers > 20 {
		return fmt.Errorf("MaxPlayers %d out of [%d,20]", c.MaxPlayers, c.MinPlayers)
	}
	if c.Countdown < 1 || c.Countdown > 600 {
		return fmt.Errorf("Countdown %d out of [1,600]", c.Countdown)
	}
	if c.ReportWindow < 1 || c.ReportWindow > 3600 {
		return fmt.Errorf("ReportWindow %d out of [1,3600]", c.ReportWindow)
	}
	return nil
}

// PlayerInfo 为在室玩家的快照信息。
type PlayerInfo struct {
	ID      string
	Ready   bool
	Host    bool
	JoinSeq int64
}

// MatchPlayerInfo 为对局名单成员的快照信息。
type MatchPlayerInfo struct {
	ID       string
	InRoom   bool // false 表示中途退出
	Reported bool
	Winner   string // 已上报的胜者（未上报为空）
}

// Snapshot 为某次 Query 返回的房间只读视图。
type Snapshot struct {
	State  State
	Now    int64
	Config Config

	Players []PlayerInfo // 在室玩家，按加入次序
	Host    string

	CountdownStart  int64 // 仅倒计时阶段有效
	CountdownExpiry int64 // 到期时刻 = 起算 + C，恰等于视为已到期

	MatchStart     int64 // 开局时刻（= 倒计时到期时刻）
	Roster         []MatchPlayerInfo
	ReportDeadline int64 // 上报期限到期时刻 = End 的 now + R
	ReportCount    int64

	Winner      string      // 仅已结束时有效
	AbortReason AbortReason // 仅已作废时有效
}
