package block

import "errors"

var (
	ErrHeader        = errors.New("block: header incomplete or invalid")
	ErrRestart       = errors.New("block: restart table incomplete")
	ErrEntry         = errors.New("block: entry data incomplete")
	ErrCRC           = errors.New("block: crc mismatch")
	ErrSharedLen     = errors.New("block: shared prefix length exceeds previous entry")
	ErrRestartOffset = errors.New("block: restart offset points into entry middle")
)
