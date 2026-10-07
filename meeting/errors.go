package meeting

import "fmt"

// Category 拒绝类别。数值越小优先级越高：一个操作同时违反多条规则时，
// 只报告类别最小（最先被检查）的那一条。
//
// 优先级：参数非法 > 时钟回退 > 房间已关闭 > 操作者不在室内 >
// 权限不足 > 目标不存在 > 状态不允许。
type Category int

const (
	CatInvalidParam    Category = iota + 1 // 参数非法
	CatClockRegression                     // 时钟回退
	CatClosed                              // 房间已关闭
	CatNotInRoom                           // 操作者不在室内
	CatPermission                          // 权限不足
	CatTargetNotFound                      // 目标不存在
	CatState                               // 状态不允许
)

func (c Category) String() string {
	switch c {
	case CatInvalidParam:
		return "invalid_param"
	case CatClockRegression:
		return "clock_regression"
	case CatClosed:
		return "room_closed"
	case CatNotInRoom:
		return "not_in_room"
	case CatPermission:
		return "permission_denied"
	case CatTargetNotFound:
		return "target_not_found"
	case CatState:
		return "state_not_allowed"
	}
	return "unknown"
}

// 可区分的拒绝原因码。
const (
	ReasonEmptyUser       = "empty_user"
	ReasonNowOutOfRange   = "now_out_of_range"
	ReasonClockBackwards  = "clock_backwards"
	ReasonRoomClosed      = "room_closed"
	ReasonNotInRoom       = "not_in_room"
	ReasonNeedHost        = "need_host"
	ReasonNeedHostOrCo    = "need_host_or_cohost"
	ReasonTargetNotFound  = "target_not_found"
	ReasonAlreadyExists   = "already_exists"
	ReasonAlreadyInQueue  = "already_in_queue"
	ReasonAlreadySpeaking = "already_speaking"
	ReasonMuted           = "muted"
	ReasonQueueFull       = "queue_full"
	ReasonNotInQueue      = "not_in_queue"
	ReasonNotSpeaker      = "not_speaker"
	ReasonSpeakerExists   = "speaker_exists"
	ReasonQueueEmpty      = "queue_empty"
	ReasonTargetIsHost    = "target_is_host"
	ReasonAlreadyCoHost   = "already_cohost"
	ReasonNotCoHost       = "not_cohost"
	ReasonMuteHost        = "mute_host"
	ReasonAlreadyMuted    = "already_muted"
	ReasonNotMuted        = "not_muted"
)

// Reject 是被拒绝操作的原因。被拒绝的操作不产生任何状态变化，
// 但通过参数与时钟检查的操作仍会先完成惰性到期处理并推进时钟。
type Reject struct {
	Cat    Category
	Reason string
}

func (e *Reject) Error() string {
	return fmt.Sprintf("rejected[%s]: %s", e.Cat, e.Reason)
}

func reject(cat Category, reason string) *Reject {
	return &Reject{Cat: cat, Reason: reason}
}
