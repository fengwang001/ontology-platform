package mux

import "errors"

var (
	// ErrDuplicateID 表示同一个 id 在未完成前被重复注册。
	ErrDuplicateID = errors.New("mux: duplicate id")
	// ErrTimedOut 表示等待者在注入时钟到达 deadline 后被淘汰。
	ErrTimedOut = errors.New("mux: timed out")
	// ErrClosed 表示匹配器已关闭。
	ErrClosed = errors.New("mux: closed")
)
