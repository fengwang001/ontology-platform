package segment

import (
	"errors"

	"ontology/event"
)

// 段文件四类损坏，全部可用 errors.Is 区分。
var (
	// ErrHeaderTruncated：文件不足一个段头（24 字节）。
	ErrHeaderTruncated = errors.New("segment: header incomplete")
	// ErrLenTruncated：某条记录的 4 字节长度前缀未写完整。
	ErrLenTruncated = errors.New("segment: length prefix incomplete")
	// ErrBodyTruncated：长度前缀完整，但载荷或 CRC 字节不足。
	ErrBodyTruncated = errors.New("segment: event body incomplete")
	// ErrCRCMismatch 复用 event 包的 CRC 错误。
	ErrCRCMismatch = event.ErrCRCMismatch
	// ErrBadMagic：段头魔数不对。
ErrBadMagic = errors.New("segment: bad magic")
)
