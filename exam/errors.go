package exam

import "errors"

// 错误分类，优先级从高到低：
//  1. ErrInvalidParam                    参数非法
//  2. ErrClockRegression                 时钟回退
//  3. ErrSessionNotFound                 会话不存在
//  4. ErrSessionEnded / ErrAlreadySettled 会话已结束或已结算
//  5. ErrInvalidToken / ErrStaleGeneration 凭证无效或代次过期
//  6. ErrAnswerWhilePaused / ErrAnswerWhileLocked / ErrNotPaused /
//     ErrNotLocked / ErrNotActive        会话状态不允许
//  7. ErrLateAnswer / ErrDuplicateAnswer 作答迟到或重复
var (
	ErrInvalidParam      = errors.New("exam: invalid parameter")
	ErrClockRegression   = errors.New("exam: server clock regression")
	ErrSessionNotFound   = errors.New("exam: session not found")
	ErrSessionExists     = errors.New("exam: session already exists")
	ErrSessionEnded      = errors.New("exam: session already ended")
	ErrAlreadySettled    = errors.New("exam: session already settled")
	ErrInvalidToken      = errors.New("exam: invalid or expired resume token")
	ErrStaleGeneration   = errors.New("exam: stale session generation")
	ErrAnswerWhilePaused = errors.New("exam: answer rejected while session paused")
	ErrAnswerWhileLocked = errors.New("exam: answer rejected while session locked")
	ErrNotPaused         = errors.New("exam: session is not paused")
	ErrNotLocked         = errors.New("exam: session is not locked")
	ErrNotActive         = errors.New("exam: session is not active")
	ErrLateAnswer        = errors.New("exam: late answer below landed watermark")
	ErrDuplicateAnswer   = errors.New("exam: duplicate answer sequence")
)
