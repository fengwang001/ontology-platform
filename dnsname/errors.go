package dnsname

import "errors"

var (
	ErrEmptyLabel        = errors.New("dnsname: empty label")
	ErrLabelTooLong      = errors.New("dnsname: label longer than 63 bytes")
	ErrNameTooLong       = errors.New("dnsname: encoded name longer than 255 bytes")
	ErrMessageTooLong    = errors.New("dnsname: message longer than 65535 bytes")
	ErrInvalidBase       = errors.New("dnsname: base offset out of range")
	ErrReservedLabel     = errors.New("dnsname: label length uses reserved high bits")
	ErrTruncated         = errors.New("dnsname: name is truncated by message end")
	ErrForwardPointer    = errors.New("dnsname: pointer does not point backward")
	ErrPointerBeforeBase = errors.New("dnsname: pointer target is before base")
)
