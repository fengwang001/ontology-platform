package termination

import "errors"

// 所有可区分的拒绝原因。被拒绝的操作不改变任何进程、令牌或计数。
var (
	ErrInvalidN          = errors.New("termination: process count must be positive")
	ErrProcessOutOfRange = errors.New("termination: process id out of range")
	ErrSendToSelf        = errors.New("termination: a process cannot send a message to itself")
	ErrIdleSender        = errors.New("termination: idle process cannot send messages")
	ErrMessageNotFound   = errors.New("termination: message not found in the network")
	ErrNoToken           = errors.New("termination: process does not hold the token")
	ErrActiveHolder      = errors.New("termination: active token holder cannot pass the token")
	ErrAlreadyIdle       = errors.New("termination: process is already idle")
	ErrTerminated        = errors.New("termination: termination already announced")
)
