package demand

import "fmt"

// Config holds the controller configuration.
type Config struct {
	// ContractDemandKW is the contracted demand in kW (positive).
	ContractDemandKW int64
	// WindowSeconds is the demand-window length in seconds (positive,
	// must be an integer multiple of SlipSeconds).
	WindowSeconds int64
	// SlipSeconds is the window slip in seconds (positive). Windows end
	// only at absolute timestamps that are multiples of the slip.
	SlipSeconds int64
	// MaxPhysicalPowerKW is the physical upper limit of average power
	// over any report interval; reports exceeding it are illegal data.
	MaxPhysicalPowerKW float64
}

func (c Config) validate() error {
	if c.ContractDemandKW <= 0 {
		return fmt.Errorf("%w: contract demand must be a positive integer, got %d", ErrInvalidParam, c.ContractDemandKW)
	}
	if c.WindowSeconds <= 0 {
		return fmt.Errorf("%w: window length must be a positive integer, got %d", ErrInvalidParam, c.WindowSeconds)
	}
	if c.SlipSeconds <= 0 {
		return fmt.Errorf("%w: slip must be a positive integer, got %d", ErrInvalidParam, c.SlipSeconds)
	}
	if c.WindowSeconds%c.SlipSeconds != 0 {
		return fmt.Errorf("%w: window length %d is not a multiple of slip %d", ErrInvalidParam, c.WindowSeconds, c.SlipSeconds)
	}
	if c.MaxPhysicalPowerKW <= 0 {
		return fmt.Errorf("%w: physical power limit must be positive, got %v", ErrInvalidParam, c.MaxPhysicalPowerKW)
	}
	return nil
}

// LoadSpec describes a controllable load.
type LoadSpec struct {
	// ID is the load number (positive, unique).
	ID int
	// RatedPowerKW is the rated power in kW (positive integer). When a
	// connected load is cut its power is assumed to drop by this amount
	// (never below zero in aggregate); when restored it rises by it.
	RatedPowerKW int64
	// Priority: larger numbers are less important. Loads whose priority
	// equals the minimum priority among all existing loads are critical
	// and are never cut.
	Priority int
	// MinOnSeconds is the minimum connected time before the load may be cut.
	MinOnSeconds int64
	// MinOffSeconds is the minimum disconnected time before the load may
	// be restored.
	MinOffSeconds int64
}

func (s LoadSpec) validate() error {
	if s.ID <= 0 {
		return fmt.Errorf("%w: load ID must be positive, got %d", ErrInvalidParam, s.ID)
	}
	if s.RatedPowerKW <= 0 {
		return fmt.Errorf("%w: rated power must be a positive integer, got %d", ErrInvalidParam, s.RatedPowerKW)
	}
	if s.Priority < 0 {
		return fmt.Errorf("%w: priority must be non-negative, got %d", ErrInvalidParam, s.Priority)
	}
	if s.MinOnSeconds < 0 {
		return fmt.Errorf("%w: min-on seconds must be non-negative, got %d", ErrInvalidParam, s.MinOnSeconds)
	}
	if s.MinOffSeconds < 0 {
		return fmt.Errorf("%w: min-off seconds must be non-negative, got %d", ErrInvalidParam, s.MinOffSeconds)
	}
	return nil
}
