package ontology

import "errors"

// 调用层面错误，按题目规定的优先级从高到低排列。
var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrCommitNotFound    = errors.New("commit not found")
	ErrTagNotFound       = errors.New("tag not found")
	ErrKeyNotFound       = errors.New("key not found")
	ErrEndorsementCycle  = errors.New("endorsement cycle")
	ErrRevocationEarlier = errors.New("revocation time earlier than allowed")
)
