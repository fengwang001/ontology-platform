// Package errs defines all sentinel errors shared by the offline license manager.
package errs

import "errors"

var (
	ErrInvalidParam  = errors.New("invalid parameter")
	ErrClockRewind   = errors.New("clock moved backwards")
	ErrAccountExists = errors.New("account already exists")
	ErrTitleExists   = errors.New("title already exists")
	ErrNoAccount     = errors.New("account not found")
	ErrNoTitle       = errors.New("title not found")
	ErrDeviceExists  = errors.New("device already registered")
	ErrDeviceFull    = errors.New("device quota full")
	ErrNoDevice      = errors.New("device not registered")
	ErrTitleEnded    = errors.New("title has been taken down")
	ErrAlreadyPlayed = errors.New("license already in playback, cannot renew")
	ErrLicenseFull   = errors.New("license quota full")
	ErrNoLicense     = errors.New("no license")
)

// Expiry reasons reported inside ExpiredError.
const (
	ReasonTitleEnded  = "title_ended"
	ReasonPlayEnded   = "playback_period_ended"
	ReasonRentalEnded = "rental_period_ended"
)

// ExpiredError wraps the reason and the expiry timestamp of a license.
type ExpiredError struct {
	Reason string
	Exp    int64
}

func (e *ExpiredError) Error() string { return "license expired: " + e.Reason }

// IsExpired reports whether err is an *ExpiredError (errors.Is support).
func IsExpired(err error) (*ExpiredError, bool) {
	var expired *ExpiredError
	if errors.As(err, &expired) {
		return expired, true
	}
	return nil, false
}
