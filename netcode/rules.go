package netcode

import "errors"

var ErrInvalidConfig = errors.New("invalid netcode configuration")

func validateConfig(config Config) error {
	if config.WorldWidth < 1 || config.WorldWidth > 1_000_000 {
		return ErrInvalidConfig
	}
	if config.Quota < 1 || config.Quota > 50 {
		return ErrInvalidConfig
	}
	if config.MaxStep < 1 || config.MaxStep > config.WorldWidth {
		return ErrInvalidConfig
	}
	if config.Backlog < 1 {
		return ErrInvalidConfig
	}
	return nil
}

func applyMove(position int64, delta int64, worldWidth int64, maxStep int64) (int64, bool) {
	if delta == 0 || delta > maxStep || delta < -maxStep {
		return position, false
	}
	position += delta
	if position < 0 {
		return 0, true
	}
	if position > worldWidth {
		return worldWidth, true
	}
	return position, true
}

func clampPosition(position int64, worldWidth int64) int64 {
	if position < 0 {
		return 0
	}
	if position > worldWidth {
		return worldWidth
	}
	return position
}
