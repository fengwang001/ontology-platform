package pumpstation

import "fmt"

func ValidateConfig(cfg Config) error {
	if cfg.PumpCount < 2 || cfg.PumpCount > 8 {
		return fmt.Errorf("%w: pump count must be between 2 and 8", ErrInvalidArgument)
	}
	if len(cfg.StartLevels) != cfg.PumpCount || len(cfg.StopLevels) != cfg.PumpCount {
		return fmt.Errorf("%w: level threshold count must equal pump count", ErrInvalidArgument)
	}
	for _, level := range cfg.StartLevels {
		if level < 0 {
			return fmt.Errorf("%w: start level must be non-negative", ErrInvalidArgument)
		}
	}
	for _, level := range cfg.StopLevels {
		if level < 0 {
			return fmt.Errorf("%w: stop level must be non-negative", ErrInvalidArgument)
		}
	}
	for k := 0; k < cfg.PumpCount; k++ {
		if cfg.StartLevels[k] <= cfg.StopLevels[k] {
			return fmt.Errorf("%w: start level %d must be above stop level %d", ErrInvalidArgument, k+1, k+1)
		}
		if k > 0 && cfg.StartLevels[k] <= cfg.StartLevels[k-1] {
			return fmt.Errorf("%w: start levels must be strictly increasing", ErrInvalidArgument)
		}
		if k > 0 && cfg.StopLevels[k] < cfg.StopLevels[k-1] {
			return fmt.Errorf("%w: stop levels must be non-decreasing", ErrInvalidArgument)
		}
	}
	if cfg.DryRunRecoveryLevel <= cfg.DryRunLevel {
		return fmt.Errorf("%w: dry-run recovery level must be above dry-run level", ErrInvalidArgument)
	}
	if cfg.OverflowLevel <= cfg.StartLevels[cfg.PumpCount-1] {
		return fmt.Errorf("%w: overflow level must be above every start level", ErrInvalidArgument)
	}
	if cfg.DryRunLevel < 0 || cfg.DryRunRecoveryLevel < 0 {
		return fmt.Errorf("%w: dry-run levels must be non-negative", ErrInvalidArgument)
	}
	if cfg.MinimumRunDuration < 0 || cfg.MinimumStopDuration < 0 || cfg.MinimumStartInterval < 0 {
		return fmt.Errorf("%w: timing constraints must be non-negative", ErrInvalidArgument)
	}
	return nil
}
