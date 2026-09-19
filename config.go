package ontology

import (
	"math"
	"time"
)

const defaultShardCount = 64

// Config configures a multi-tenant token bucket limiter.
//
// All tenants share the same tokens-per-second rate and bucket capacity.
// Zero IdleTTL disables inactive-tenant eviction. ShardCount is rounded up to
// the next power of two when it is positive.
type Config struct {
	Rate       int
	Capacity   int
	IdleTTL    time.Duration
	ShardCount int
}

func (cfg Config) normalized() (Config, bool) {
	if cfg.Rate <= 0 || cfg.Capacity <= 0 || cfg.IdleTTL < 0 {
		return Config{}, false
	}
	if int64(cfg.Capacity) > math.MaxInt64/tokenScale {
		return Config{}, false
	}
	if cfg.ShardCount <= 0 {
		cfg.ShardCount = defaultShardCount
	}
	if cfg.ShardCount > 1<<16 {
		return Config{}, false
	}
	cfg.ShardCount = nextPowerOfTwo(cfg.ShardCount)
	return cfg, true
}

func nextPowerOfTwo(value int) int {
	power := 1
	for power < value {
		power <<= 1
	}
	return power
}
