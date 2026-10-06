package booking

import "fmt"

type Config struct {
	LongThreshold  int64
	ShortThreshold int64
	RefundRates    Rates
	ChangeRates    Rates
	ChangeLimit    int
	VoucherTTL     int64
}

type Rates struct {
	Long  int
	Mid   int
	Short int
}

type Tier int

const (
	TierLong Tier = iota
	TierMid
	TierShort
)

func (t Tier) String() string {
	switch t {
	case TierLong:
		return "long"
	case TierMid:
		return "middle"
	default:
		return "short"
	}
}

func validateConfig(cfg Config) error {
	if cfg.LongThreshold <= cfg.ShortThreshold || cfg.ShortThreshold <= 0 {
		return errorf(ErrInvalidArgument, "require long threshold > short threshold > 0")
	}
	if err := validateRates(cfg.RefundRates); err != nil {
		return err
	}
	if err := validateRates(cfg.ChangeRates); err != nil {
		return err
	}
	if cfg.ChangeLimit < 0 || cfg.VoucherTTL < 0 {
		return errorf(ErrInvalidArgument, "negative limit or voucher ttl")
	}
	return nil
}

func validateRates(rates Rates) error {
	if rates.Long < 0 || rates.Long > 100 || rates.Mid < 0 || rates.Mid > 100 || rates.Short < 0 || rates.Short > 100 {
		return errorf(ErrInvalidArgument, "rate must be in [0,100]")
	}
	return nil
}

func tier(delta int64, cfg Config) Tier {
	switch {
	case delta >= cfg.LongThreshold:
		return TierLong
	case delta >= cfg.ShortThreshold:
		return TierMid
	default:
		return TierShort
	}
}

func (r Rates) rate(tier Tier) int {
	switch tier {
	case TierLong:
		return r.Long
	case TierMid:
		return r.Mid
	default:
		return r.Short
	}
}

func fee(price int64, rate int) int64 {
	return (price*int64(rate) + 99) / 100
}

func formatMsg(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
