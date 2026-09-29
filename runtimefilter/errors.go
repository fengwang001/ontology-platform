package runtimefilter

import "errors"

var (
	// ErrUnknownJoin 连接类型未知，无法推导过滤安全性。
	ErrUnknownJoin = errors.New("runtimefilter: unknown join type")
	// ErrShardOutOfRange 分片编号越界。
	ErrShardOutOfRange = errors.New("runtimefilter: shard index out of range")
	// ErrDuplicateShard 同一分片重复报告。
	ErrDuplicateShard = errors.New("runtimefilter: duplicate shard report")
)
