package battery

import "errors"

var (
	ErrInvalidConfig        = errors.New("battery: invalid configuration")
	ErrInvalidSample        = errors.New("battery: invalid sample (count mismatch or value out of range)")
	ErrTimeNotAdvancing     = errors.New("battery: sample time must be strictly greater than the last accepted time")
	ErrNoPermission         = errors.New("battery: operator not permitted to reset")
	ErrNotLatched           = errors.New("battery: no latched fault to reset")
	ErrNoSampleSinceLatch   = errors.New("battery: at least one accepted sample is required after latch before reset")
	ErrRecoveryNotSatisfied = errors.New("battery: recovery conditions are not satisfied")
)
