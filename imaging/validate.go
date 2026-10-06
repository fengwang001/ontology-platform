package imaging

const (
	minTime = 0
	maxTime = 10_000_000
	dayMin  = 1440
)

func nonemptyID(s string) bool { return s != "" }

func validTime(t int) bool { return minTime <= t && t <= maxTime }

// validateConfig 校验配置本身（非正参数视为非法）。
func validateConfig(cfg Config) error {
	if cfg.ValidityNormal <= 0 || cfg.ValidityHighRisk <= 0 ||
		cfg.KidneyLow <= 0 || cfg.KidneyHigh <= 0 || cfg.KidneyLow >= cfg.KidneyHigh ||
		cfg.HydrationLead <= 0 || cfg.PremedicationLead <= 0 ||
		cfg.ObservationMinutes <= 0 || cfg.ObservationCapacity <= 0 ||
		cfg.CleaningMinutes == nil {
		return ErrInvalidArgument
	}
	if d, ok := cfg.CleaningMinutes[ClassCT]; !ok || d <= 0 {
		return ErrInvalidArgument
	}
	if d, ok := cfg.CleaningMinutes[ClassMR]; !ok || d <= 0 {
		return ErrInvalidArgument
	}
	return nil
}
