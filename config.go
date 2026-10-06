package ftl

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrUnwritten       = errors.New("logical page is not written")
	ErrNoSpace         = errors.New("flash space exhausted")
)

type Config struct {
	BlockCount    int
	PagesPerBlock int
	LogicalPages  int
	LowWatermark  int
	HighWatermark int
	EraseLimit    int
	WearThreshold int
}

func (c Config) Validate() error {
	if c.BlockCount <= 0 || c.PagesPerBlock <= 0 || c.LogicalPages <= 0 || c.EraseLimit <= 0 {
		return ErrInvalidArgument
	}
	if c.LowWatermark < 2 || c.HighWatermark <= c.LowWatermark || c.HighWatermark >= c.BlockCount {
		return ErrInvalidArgument
	}
	if c.WearThreshold < 0 {
		return ErrInvalidArgument
	}
	return nil
}
