package ontology

import "errors"

// 参数非法（登记 / BeginMerge）。
var (
	ErrEmptyBatch   = errors.New("ontology: batch is empty")
	ErrEmptyKey     = errors.New("ontology: document key is empty")
	ErrEmptyTerm    = errors.New("ontology: term is empty")
	ErrInvalidMerge = errors.New("ontology: merge ids must contain at least two distinct segments")
)

// 其余可区分的拒绝原因。
var (
	ErrDuplicateKey    = errors.New("ontology: key conflicts with a live document")
	ErrSegmentNotFound = errors.New("ontology: segment not found")
	ErrSegmentBusy     = errors.New("ontology: segment is part of an unfinished merge")
	ErrKeyNotFound     = errors.New("ontology: no live document with that key")
	ErrInvalidHandle   = errors.New("ontology: merge handle is unknown, committed or aborted")
)
