package floorcontrol

import "errors"

// 拒绝原因。所有错误彼此不同，调用方可据此区分具体拒绝原因。
var (
	// 参数非法（空标识、S/Q 越界、now 越界等）。
	ErrInvalidArgument = errors.New("floorcontrol: invalid argument")
	// 时钟回退：now 小于上一次被接受操作的 now。
	ErrClockBack = errors.New("floorcontrol: clock moved backwards")
	// 房间已关闭。
	ErrClosed = errors.New("floorcontrol: room closed")
	// 操作者不在室内。
	ErrOperatorNotInRoom = errors.New("floorcontrol: operator not in room")
	// 权限不足。
	ErrPermissionDenied = errors.New("floorcontrol: permission denied")
	// 目标成员不存在。
	ErrTargetNotFound = errors.New("floorcontrol: target member not found")

	// —— 状态类拒绝（彼此可区分）——
	ErrAlreadyInRoom    = errors.New("floorcontrol: user already in room")
	ErrAlreadyRaised    = errors.New("floorcontrol: user already in queue")
	ErrAlreadySpeaker   = errors.New("floorcontrol: user already speaking")
	ErrRaisedWhileMuted = errors.New("floorcontrol: muted user cannot raise hand")
	ErrQueueFull        = errors.New("floorcontrol: raise queue full")
	ErrSpeakerActive    = errors.New("floorcontrol: another speaker currently holds the floor")
	ErrQueueEmpty       = errors.New("floorcontrol: raise queue empty")
	ErrNotInQueue       = errors.New("floorcontrol: user is not in queue")
	ErrNotSpeaker       = errors.New("floorcontrol: user is not the current speaker")
	ErrIsHost           = errors.New("floorcontrol: target is the host")
	ErrRoleUnchanged    = errors.New("floorcontrol: target already has that role")
	ErrAlreadyMuted     = errors.New("floorcontrol: target already muted")
	ErrNotMuted         = errors.New("floorcontrol: target is not muted")
)
