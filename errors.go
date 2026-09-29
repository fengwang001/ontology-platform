package doublewrite

import "errors"

var (
	ErrEmptyBatch        = errors.New("doublewrite: batch is empty")
	ErrBatchTooLarge     = errors.New("doublewrite: batch exceeds doublewrite area capacity")
	ErrDuplicatePage     = errors.New("doublewrite: duplicate page number in batch")
	ErrPageOutOfRange    = errors.New("doublewrite: page number out of range")
	ErrInvalidPage       = errors.New("doublewrite: invalid page payload")
	ErrVersionNotNewer   = errors.New("doublewrite: new version must be greater than in-place version")
	ErrPageCorrupt       = errors.New("doublewrite: page checksum mismatch")
	ErrMarkerCorrupt     = errors.New("doublewrite: completion marker checksum mismatch")
	ErrPowerCut          = errors.New("doublewrite: simulated power cut")
	ErrUnrecoverablePage = errors.New("doublewrite: page corrupt with no usable copy")
)
