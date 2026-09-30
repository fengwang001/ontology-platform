package autoscaler

import (
	"errors"
	"log/slog"
	"math"
	"math/big"
	"sync"
)

var (
	ErrInvalidMinReplicas = errors.New("min replicas must be at least 1")
	ErrInvalidMaxReplicas = errors.New("max replicas must not be less than min replicas")
	ErrInvalidTarget      = errors.New("target must be positive")
	ErrInvalidWindow      = errors.New("scale-down window must not be negative")
	ErrInvalidCooldown    = errors.New("scale-down cooldown must not be negative")
	ErrEvaluationOrder    = errors.New("evaluation time must not be earlier than the previous evaluation time")
	ErrNegativeMetric     = errors.New("metric must not be negative")
)

type Decision string

const (
	DecisionScaleUp         Decision = "scale_up"
	DecisionScaleDown       Decision = "scale_down"
	DecisionMaintain        Decision = "maintain"
	DecisionWindowBlocked   Decision = "window_blocked"
	DecisionCooldownBlocked Decision = "cooldown_blocked"
)

type Evaluation struct {
	Replicas int64
	Decision Decision
}

type recommendation struct {
	at       int64
	replicas int64
}

type Controller struct {
	mu             sync.Mutex
	minReplicas    int64
	maxReplicas    int64
	target         int64
	window         int64
	cooldown       int64
	replicasValue  int64
	lastEvaluation int64
	hasEvaluation  bool
	lastScaleDown  int64
	hasScaleDown   bool
	history        []recommendation
}

type Config struct {
	MinReplicas int64
	MaxReplicas int64
	Target      int64
	WindowMS    int64
	CooldownMS  int64
}

func New(config Config) (*Controller, error) {
	if config.MinReplicas < 1 {
		return nil, ErrInvalidMinReplicas
	}
	if config.MaxReplicas < config.MinReplicas {
		return nil, ErrInvalidMaxReplicas
	}
	if config.Target <= 0 {
		return nil, ErrInvalidTarget
	}
	if config.WindowMS < 0 {
		return nil, ErrInvalidWindow
	}
	if config.CooldownMS < 0 {
		return nil, ErrInvalidCooldown
	}

	return &Controller{
		minReplicas:   config.MinReplicas,
		maxReplicas:   config.MaxReplicas,
		target:        config.Target,
		window:        config.WindowMS,
		cooldown:      config.CooldownMS,
		replicasValue: config.MinReplicas,
	}, nil
}

func (c *Controller) Evaluate(now int64, metric int64) (Evaluation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasEvaluation && now < c.lastEvaluation {
		slog.Warn("replica evaluation rejected",
			"now", now,
			"metric", metric,
			"previous_now", c.lastEvaluation,
			"output_replicas", c.replicasValue,
			"decision", "rejected",
			"reason", ErrEvaluationOrder,
		)
		return Evaluation{}, ErrEvaluationOrder
	}
	if metric < 0 {
		slog.Warn("replica evaluation rejected",
			"now", now,
			"metric", metric,
			"output_replicas", c.replicasValue,
			"decision", "rejected",
			"reason", ErrNegativeMetric,
		)
		return Evaluation{}, ErrNegativeMetric
	}

	current := c.replicasValue
	recommended := c.recommend(current, metric)
	c.history = append(c.history, recommendation{at: now, replicas: recommended})

	windowStart := int64(math.MinInt64)
	if now > math.MinInt64+c.window {
		windowStart = now - c.window
	}

	windowMaximum := int64(math.MinInt64)
	pruned := c.history[:0]
	for _, item := range c.history {
		if item.at < windowStart {
			continue
		}
		pruned = append(pruned, item)
		if item.replicas > windowMaximum {
			windowMaximum = item.replicas
		}
	}
	c.history = pruned

	target := min(current, windowMaximum)
	decision := DecisionMaintain
	result := current
	reason := "recommended replicas are not below current replicas"

	switch {
	case recommended > current:
		doubled := c.maxReplicas
		if current <= c.maxReplicas/2 {
			doubled = current * 2
		}
		result = min(recommended, doubled)
		decision = DecisionScaleUp
		reason = "scale up is limited to doubling current replicas"
	case target == current:
		result = current
		if recommended < current {
			decision = DecisionWindowBlocked
			reason = "a higher recommendation remains inside the scale-down window"
		}
	case c.hasScaleDown && !c.cooldownElapsed(now):
		result = current
		decision = DecisionCooldownBlocked
		reason = "scale-down cooldown has not elapsed"
	default:
		result = target
		decision = DecisionScaleDown
		c.lastScaleDown = now
		c.hasScaleDown = true
		reason = "window target is below current replicas and cooldown has elapsed"
	}

	c.replicasValue = result
	c.lastEvaluation = now
	c.hasEvaluation = true

	slog.Info("replica recommendation evaluated",
		"now", now,
		"metric", metric,
		"current_replicas", current,
		"recommended_replicas", recommended,
		"window_maximum", windowMaximum,
		"target_replicas", target,
		"new_replicas", result,
		"decision", decision,
		"reason", reason,
	)

	return Evaluation{Replicas: result, Decision: decision}, nil
}

func (c *Controller) Replicas() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.replicasValue
}

func (c *Controller) LastScaleDownAt() (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastScaleDown, c.hasScaleDown
}

func (c *Controller) recommend(current int64, metric int64) int64 {
	distance := new(big.Int).Sub(big.NewInt(metric), big.NewInt(c.target))
	distance.Abs(distance)
	distance.Mul(distance, big.NewInt(10))
	if distance.Cmp(big.NewInt(c.target)) <= 0 {
		return current
	}

	numerator := new(big.Int).Mul(big.NewInt(current), big.NewInt(metric))
	quotient, remainder := new(big.Int).QuoRem(numerator, big.NewInt(c.target), new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}

	if !quotient.IsInt64() {
		return c.maxReplicas
	}
	return c.clamp(quotient.Int64())
}

func (c *Controller) clamp(replicas int64) int64 {
	if replicas < c.minReplicas {
		return c.minReplicas
	}
	if replicas > c.maxReplicas {
		return c.maxReplicas
	}
	return replicas
}

func (c *Controller) cooldownElapsed(now int64) bool {
	if c.lastScaleDown <= math.MaxInt64-c.cooldown {
		return now >= c.lastScaleDown+c.cooldown
	}
	return false
}
