package bm25

import "errors"

var (
	ErrInvalidArgument   = errors.New("bm25: invalid argument")
	ErrDuplicateDocument = errors.New("bm25: duplicate document")
	ErrDocumentNotFound  = errors.New("bm25: document not found")
	ErrVersionOutOfRange = errors.New("bm25: version out of range")
	ErrWatermarkRollback = errors.New("bm25: watermark rollback")
	ErrVersionCompacted  = errors.New("bm25: version compacted")
)
