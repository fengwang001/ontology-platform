package offline

import (
	"errors"

	"ontology/playback"
)

// 可通过 errors.Is 区分的哨兵错误。
var (
	ErrInvalidArg       = errors.New("offline: invalid argument")
	ErrClockRollback    = errors.New("offline: clock moved backwards")
	ErrAccountNotFound  = errors.New("offline: account not found")
	ErrAccountExists    = errors.New("offline: account already exists")
	ErrTitleNotFound    = errors.New("offline: title not found")
	ErrTitleExists      = errors.New("offline: title already exists")
	ErrDeviceRegistered = errors.New("offline: device already registered")
	ErrDeviceNotFound   = errors.New("offline: device not registered")
	ErrDeviceFull       = errors.New("offline: device quota full")
	ErrTitleOffShelf    = errors.New("offline: title off shelf")
	ErrAlreadyPlaying   = errors.New("offline: license already started, cannot renew")
	ErrLicenseFull      = errors.New("offline: license quota full")
	ErrNoLicense        = errors.New("offline: no license")
)

// ExpiredError 表示许可已过期；errors.As 取出后可读 Reason/Exp。
type ExpiredError struct {
	Reason playback.Reason
	Exp    int64
}

func (e *ExpiredError) Error() string {
	switch e.Reason {
	case playback.ReasonTitleOffShelf:
		return "offline: license expired (title off shelf)"
	case playback.ReasonPlaybackEnded:
		return "offline: license expired (playback period ended)"
	default:
		return "offline: license expired (rental ended)"
	}
}

// Is 支持 errors.Is(err, ErrExpired) 这一粗粒度匹配。
func (e *ExpiredError) Is(target error) bool { return target == ErrExpired }

// ErrExpired 是过期错误的粗粒度哨兵。
var ErrExpired = errors.New("offline: license expired")
