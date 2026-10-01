package ontology

import (
	"math/big"
	"sync"
)

type StallState int

const (
	StallNormal StallState = iota
	StallSlow
	StallStopped
)

type StallRejectReason int

const (
	StallRejectNone StallRejectReason = iota
	StallRejectS1
	StallRejectP1
	StallRejectImm
	StallRejectD
	StallRejectN0
	StallRejectPending
	StallRejectFrozen
	StallRejectWriteStopped
)

type StallConfig struct {
	Level0Slow     int64
	Level0Stop     int64
	PendingSlow    int64
	PendingStop    int64
	FrozenStop     int64
	MaxDelayMicros int64
}

type StallResult struct {
	State       StallState
	DelayMicros int64
	Rejected    bool
	Reason      StallRejectReason
}

type WriteStallController struct {
	mu          sync.RWMutex
	level0Slow  int64
	level0Stop  int64
	pendingSlow int64
	pendingStop int64
	frozenStop  int64
	maxDelay    int64
	state       StallState
	delayMicros int64
}

func NewWriteStallController(config StallConfig) (*WriteStallController, StallRejectReason) {
	switch {
	case config.Level0Slow >= config.Level0Stop:
		return nil, StallRejectS1
	case config.PendingSlow >= config.PendingStop:
		return nil, StallRejectP1
	case config.FrozenStop <= 0:
		return nil, StallRejectImm
	case config.MaxDelayMicros <= 0:
		return nil, StallRejectD
	default:
		return &WriteStallController{
			level0Slow:  config.Level0Slow,
			level0Stop:  config.Level0Stop,
			pendingSlow: config.PendingSlow,
			pendingStop: config.PendingStop,
			frozenStop:  config.FrozenStop,
			maxDelay:    config.MaxDelayMicros,
			state:       StallNormal,
		}, StallRejectNone
	}
}

func (c *WriteStallController) Observe(level0Files int64, pendingBytes int64, frozenMemtables int64) (result StallResult) {
	switch {
	case level0Files < 0:
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.currentResult(StallRejectN0)
	case pendingBytes < 0:
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.currentResult(StallRejectPending)
	case frozenMemtables < 0:
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.currentResult(StallRejectFrozen)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	stopByLevel0 := level0Files >= c.level0Stop
	stopByPending := pendingBytes >= c.pendingStop
	stopByFrozen := frozenMemtables >= c.frozenStop
	if stopByLevel0 || stopByPending || stopByFrozen {
		c.state = StallStopped
		c.delayMicros = 0
		return c.currentResult(StallRejectNone)
	}

	slowCondition := level0Files >= c.level0Slow || pendingBytes >= c.pendingSlow
	if c.state == StallStopped {
		level0BelowRecovery := belowRecovery(level0Files, c.level0Stop)
		pendingBelowRecovery := belowRecovery(pendingBytes, c.pendingStop)
		frozenBelowRecovery := belowRecovery(frozenMemtables, c.frozenStop)
		if level0BelowRecovery && pendingBelowRecovery && frozenBelowRecovery {
			if slowCondition {
				c.state = StallSlow
				c.delayMicros = slowDelay(c.maxDelay, level0Files, pendingBytes, c.level0Slow, c.level0Stop, c.pendingSlow, c.pendingStop)
			} else {
				c.state = StallNormal
				c.delayMicros = 0
			}
		}
		return c.currentResult(StallRejectNone)
	}

	if slowCondition {
		c.state = StallSlow
		c.delayMicros = slowDelay(c.maxDelay, level0Files, pendingBytes, c.level0Slow, c.level0Stop, c.pendingSlow, c.pendingStop)
		return c.currentResult(StallRejectNone)
	}

	if c.state == StallSlow {
		level0BelowRecovery := belowRecovery(level0Files, c.level0Slow)
		pendingBelowRecovery := belowRecovery(pendingBytes, c.pendingSlow)
		if level0BelowRecovery && pendingBelowRecovery {
			c.state = StallNormal
			c.delayMicros = 0
		} else {
			c.delayMicros = slowDelay(c.maxDelay, level0Files, pendingBytes, c.level0Slow, c.level0Stop, c.pendingSlow, c.pendingStop)
		}
	}

	return c.currentResult(StallRejectNone)
}

func (c *WriteStallController) Admit() StallResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.state == StallStopped {
		return StallResult{
			State:       c.state,
			DelayMicros: 0,
			Rejected:    true,
			Reason:      StallRejectWriteStopped,
		}
	}
	return c.currentResult(StallRejectNone)
}

func (c *WriteStallController) State() StallState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *WriteStallController) DelayMicros() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.delayMicros
}

func (c *WriteStallController) currentResult(reason StallRejectReason) StallResult {
	return StallResult{
		State:       c.state,
		DelayMicros: c.delayMicros,
		Rejected:    reason != StallRejectNone,
		Reason:      reason,
	}
}

func belowRecovery(value int64, threshold int64) bool {
	valueBig := big.NewInt(value)
	recovery := new(big.Int).Mul(big.NewInt(3), big.NewInt(threshold))
	recovery.Quo(recovery, big.NewInt(4))
	return valueBig.Cmp(recovery) < 0
}

func slowDelay(maxDelay int64, level0Files int64, pendingBytes int64, level0Slow int64, level0Stop int64, pendingSlow int64, pendingStop int64) int64 {
	level0Numerator := new(big.Int).Sub(big.NewInt(level0Files), big.NewInt(level0Slow))
	level0Denominator := new(big.Int).Sub(big.NewInt(level0Stop), big.NewInt(level0Slow))
	pendingNumerator := new(big.Int).Sub(big.NewInt(pendingBytes), big.NewInt(pendingSlow))
	pendingDenominator := new(big.Int).Sub(big.NewInt(pendingStop), big.NewInt(pendingSlow))

	numerator := level0Numerator
	denominator := level0Denominator
	level0ComparedToPending := new(big.Int).Mul(level0Numerator, pendingDenominator)
	pendingComparedToLevel0 := new(big.Int).Mul(pendingNumerator, level0Denominator)
	if level0ComparedToPending.Cmp(pendingComparedToLevel0) < 0 {
		numerator = pendingNumerator
		denominator = pendingDenominator
	}

	zero := big.NewInt(0)
	if numerator.Cmp(zero) <= 0 {
		return 1
	}
	if numerator.Cmp(denominator) >= 0 {
		return maxDelay
	}

	scaledNumerator := new(big.Int).Mul(big.NewInt(maxDelay), numerator)
	delay := new(big.Int).Quo(scaledNumerator, denominator)
	remainder := new(big.Int).Rem(scaledNumerator, denominator)
	if remainder.Cmp(zero) != 0 {
		delay.Add(delay, big.NewInt(1))
	}
	if delay.Cmp(big.NewInt(1)) < 0 {
		return 1
	}
	return delay.Int64()
}

func (state StallState) String() string {
	switch state {
	case StallSlow:
		return "slow"
	case StallStopped:
		return "stopped"
	default:
		return "normal"
	}
}

func (reason StallRejectReason) String() string {
	switch reason {
	case StallRejectS1:
		return "S1 must be less than S2"
	case StallRejectP1:
		return "P1 must be less than P2"
	case StallRejectImm:
		return "I must be positive"
	case StallRejectD:
		return "D must be positive"
	case StallRejectN0:
		return "n0 must not be negative"
	case StallRejectPending:
		return "pending bytes must not be negative"
	case StallRejectFrozen:
		return "frozen memtable count must not be negative"
	case StallRejectWriteStopped:
		return "writes are stopped"
	default:
		return "accepted"
	}
}
