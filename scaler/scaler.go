package scaler

import (
	"errors"
	"io"
	"log"
	"math"
	"os"
	"sync"
)

// Decision 表示一次评估后控制器的判定类别。
type Decision string

const (
	// DecisionScaleUp 表示扩容，受翻倍限速。
	DecisionScaleUp Decision = "scale_up"
	// DecisionScaleDown 表示实际发生缩容。
	DecisionScaleDown Decision = "scale_down"
	// DecisionHold 表示目标与当前一致或无需调整。
	DecisionHold Decision = "hold"
	// DecisionWindowBlocked 表示推荐值更小但稳定窗口内的高推荐值压住了缩容。
	DecisionWindowBlocked Decision = "window_blocked"
	// DecisionCooldownBlocked 表示缩容目标已低于当前但仍处于缩容冷却期。
	DecisionCooldownBlocked Decision = "cooldown_blocked"
)

// Scaler 是带稳定窗口与缩容冷却的副本数弹性伸缩控制器。
type Scaler struct {
	mu             sync.RWMutex
	minReplicas    int64
	maxReplicas    int64
	target         int64
	window         int64
	cooldown       int64
	current        int64
	history        []sample
	lastDownAt     int64
	hasScaledDown  bool
	lastEvaluateAt int64
	hasEvaluated   bool
	logger         *log.Logger
}

type sample struct {
	at      int64
	replica int64
}

// Option 配置控制器的可选参数。
type Option func(*Scaler)

// WithLogger 设置评估日志输出位置，nil 表示关闭日志。
func WithLogger(w io.Writer) Option {
	return func(s *Scaler) {
		if w == nil {
			s.logger = log.New(io.Discard, "", 0)
		} else {
			s.logger = log.New(w, "[scaler] ", log.LstdFlags|log.Lmicroseconds)
		}
	}
}

var (
	// ErrMinReplicas 表示下限小于 1。
	ErrMinReplicas = errors.New("scaler: minReplicas must be >= 1")
	// ErrMaxReplicas 表示上限小于下限。
	ErrMaxReplicas = errors.New("scaler: maxReplicas must be >= minReplicas")
	// ErrTarget 表示目标值不为正。
	ErrTarget = errors.New("scaler: target must be positive")
	// ErrWindow 表示缩容窗口为负。
	ErrWindow = errors.New("scaler: windowMillis must be >= 0")
	// ErrCooldown 表示缩容冷却为负。
	ErrCooldown = errors.New("scaler: cooldownMillis must be >= 0")
	// ErrTimeRegression 表示评估时刻早于上一次评估时刻。
	ErrTimeRegression = errors.New("scaler: now must not be earlier than previous evaluation")
	// ErrNegativeMetric 表示指标值为负。
	ErrNegativeMetric = errors.New("scaler: metric must be >= 0")
)

// New 创建控制器。初始副本数等于下限。
func New(minReplicas, maxReplicas int64, target, windowMillis, cooldownMillis int64, opts ...Option) (*Scaler, error) {
	switch {
	case minReplicas < 1:
		return nil, ErrMinReplicas
	case maxReplicas < minReplicas:
		return nil, ErrMaxReplicas
	case target <= 0:
		return nil, ErrTarget
	case windowMillis < 0:
		return nil, ErrWindow
	case cooldownMillis < 0:
		return nil, ErrCooldown
	}
	s := &Scaler{
		minReplicas: minReplicas,
		maxReplicas: maxReplicas,
		target:      target,
		window:      windowMillis,
		cooldown:    cooldownMillis,
		current:     minReplicas,
		logger:      log.New(os.Stderr, "[scaler] ", log.LstdFlags|log.Lmicroseconds),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Evaluate 在给定时刻按指标值评估并推进副本数。
//
// 判定流程：
//  1. 指标与目标值相差不超过 10%（含边界）时推荐值等于当前副本数，
//     否则推荐值为 ceil(当前副本数 * 指标 / 目标值)，再夹到上下限内；
//  2. 推荐值大于当前值时扩容，一次最多翻倍；
//  3. 推荐值不大于当前值时，缩容目标为当前值与窗口内（含本次）
//     最大推荐值的较小者；目标等于当前值则为窗口压住或维持；
//  4. 目标低于当前值但距上次实际缩容不足冷却时长则为冷却压住。
//
// 非法输入（时刻回退、指标为负）整体拒绝，不改变任何状态。
func (s *Scaler) Evaluate(nowMillis, metric int64) (replicas int64, decision Decision, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasEvaluated && nowMillis < s.lastEvaluateAt {
		s.logger.Printf("reject now=%d metric=%d: %v (lastEvaluateAt=%d)",
			nowMillis, metric, ErrTimeRegression, s.lastEvaluateAt)
		return s.current, "", ErrTimeRegression
	}
	if metric < 0 {
		s.logger.Printf("reject now=%d metric=%d: %v", nowMillis, metric, ErrNegativeMetric)
		return s.current, "", ErrNegativeMetric
	}
	s.lastEvaluateAt = nowMillis
	s.hasEvaluated = true

	current := s.current

	// 计算推荐值：|m-T|*10 <= T（即偏差不超过 10%，含边界）时保持当前值。
	var recommended int64
	diff := metric - s.target
	if diff < 0 {
		diff = -diff
	}
	if diff <= s.target/10 {
		recommended = current
	} else {
		recommended = ceilDivRatio(current, metric, s.target)
	}

	// 夹到上下限之内。
	clamped := recommended
	if clamped < s.minReplicas {
		clamped = s.minReplicas
	}
	if clamped > s.maxReplicas {
		clamped = s.maxReplicas
	}

	// 清理窗口外历史（now-t > W 移出），随后记录本次推荐值。
	cutoff := nowMillis - s.window
	maxInWindow := clamped
	n := 0
	for _, h := range s.history {
		if h.at >= cutoff {
			s.history[n] = h
			n++
			if h.replica > maxInWindow {
				maxInWindow = h.replica
			}
		}
	}
	s.history = append(s.history[:n], sample{at: nowMillis, replica: clamped})

	switch {
	case clamped > current:
		// 扩容：翻倍限速，不受窗口与冷却影响。
		next := 2 * current
		if clamped < next {
			next = clamped
		}
		s.current = next
		decision = DecisionScaleUp
		s.logger.Printf("input now=%d metric=%d current=%d recommended=%d clamped=%d -> output=%d decision=%s reason=scale_up_capped_at_double",
			nowMillis, metric, current, recommended, clamped, next, decision)
		return next, decision, nil
	}

	// 不扩容：目标为当前值与窗口内最大推荐值的较小者。
	targetReplicas := current
	if maxInWindow < targetReplicas {
		targetReplicas = maxInWindow
	}

	switch {
	case targetReplicas == current:
		if clamped < current {
			decision = DecisionWindowBlocked
			s.logger.Printf("input now=%d metric=%d current=%d recommended=%d clamped=%d windowMax=%d -> output=%d decision=%s reason=window_high_watermark_holds",
				nowMillis, metric, current, recommended, clamped, maxInWindow, current, decision)
		} else {
			decision = DecisionHold
			s.logger.Printf("input now=%d metric=%d current=%d recommended=%d clamped=%d windowMax=%d -> output=%d decision=%s reason=within_tolerance_or_equal_target",
				nowMillis, metric, current, recommended, clamped, maxInWindow, current, decision)
		}
		s.current = current
		return current, decision, nil
	case s.hasScaledDown && nowMillis-s.lastDownAt < s.cooldown:
		decision = DecisionCooldownBlocked
		s.logger.Printf("input now=%d metric=%d current=%d recommended=%d clamped=%d windowMax=%d target=%d -> output=%d decision=%s reason=cooldown_remaining=%dms",
			nowMillis, metric, current, recommended, clamped, maxInWindow, targetReplicas, current, decision,
			s.cooldown-(nowMillis-s.lastDownAt))
		return current, decision, nil
	default:
		s.current = targetReplicas
		s.lastDownAt = nowMillis
		s.hasScaledDown = true
		decision = DecisionScaleDown
		s.logger.Printf("input now=%d metric=%d current=%d recommended=%d clamped=%d windowMax=%d target=%d -> output=%d decision=%s reason=scale_down_to_window_target",
			nowMillis, metric, current, recommended, clamped, maxInWindow, targetReplicas, targetReplicas, decision)
		return targetReplicas, decision, nil
	}
}

// Replicas 返回当前副本数。
func (s *Scaler) Replicas() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// LastScaleDownAt 返回上次实际缩容时刻；从未缩容时第二返回值为 false。
func (s *Scaler) LastScaleDownAt() (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastDownAt, s.hasScaledDown
}

// ceilDivRatio 返回 ceil(c*m/T)，中间乘法溢出时退化为浮点近似。
func ceilDivRatio(c, m, t int64) int64 {
	if c == 0 || m == 0 {
		return 0
	}
	if c <= math.MaxInt64/m {
		return (c*m + t - 1) / t
	}
	v := math.Ceil(float64(c) * float64(m) / float64(t))
	if v < 0 || v > float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(v)
}
