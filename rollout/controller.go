package rollout

import "errors"

// State 表示特性渐进发布控制器的生命周期状态。
type State int

const (
	Idle State = iota
	Running
	Paused
	Completed
	RolledBack
)

// Step 是发布阶梯：百分比（1..100）与保持时长（毫秒）。
type Step struct {
	Percent int
	HoldMs  int64
}

// Transition 表示一次阶段跨越。
type Transition struct {
	From int
	To   int
	AtMs int64
}

// StatusSnapshot 是 Status 的返回值。
type StatusSnapshot struct {
	State   State
	Step    int
	Percent int
}

// 可区分的拒绝原因。
var (
	ErrEmptySalt                    = errors.New("rollout: salt must be a non-empty string")
	ErrTooFewSteps                  = errors.New("rollout: at least two steps are required")
	ErrPercentOutOfRange            = errors.New("rollout: step percent must be an integer in [1,100]")
	ErrPercentNotStrictlyIncreasing = errors.New("rollout: step percents must be strictly increasing")
	ErrNegativeHold                 = errors.New("rollout: step hold must not be negative")
	ErrClockRewind                  = errors.New("rollout: now must not be earlier than a previously accepted operation")
	ErrInvalidState                 = errors.New("rollout: operation not permitted in current state")
	ErrEmptyUser                    = errors.New("rollout: user must be a non-empty string")
)

// Controller 是带注入时刻的特性渐进发布控制器。
type Controller struct {
	salt  string
	steps []Step

	mu        chan struct{}
	state     State
	level     int
	enteredAt int64
	paused    int64
	pauseAt   int64
	maxNow    int64
	haveNow   bool
}

// New 构造控制器并按顺序校验参数。
func New(salt string, steps []Step) (*Controller, error) {
	if salt == "" {
		return nil, ErrEmptySalt
	}
	if len(steps) < 2 {
		return nil, ErrTooFewSteps
	}
	for _, step := range steps {
		if step.Percent < 1 || step.Percent > 100 {
			return nil, ErrPercentOutOfRange
		}
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].Percent <= steps[i-1].Percent {
			return nil, ErrPercentNotStrictlyIncreasing
		}
	}
	for _, step := range steps {
		if step.HoldMs < 0 {
			return nil, ErrNegativeHold
		}
	}
	copied := make([]Step, len(steps))
	copy(copied, steps)
	return &Controller{
		salt:  salt,
		steps: copied,
		mu:    make(chan struct{}, 1),
		state: Idle,
		level: -1,
	}, nil
}

// Start 仅 Idle 或 RolledBack 可调用。
func (c *Controller) Start(now int64) error {
	c.lock()
	defer c.unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if c.state != Idle && c.state != RolledBack {
		return ErrInvalidState
	}
	c.commitClock(now)
	c.state = Running
	c.level = 0
	c.enteredAt = now
	c.paused = 0
	c.pauseAt = 0
	return nil
}

// Pause 仅 Running 可调用。
func (c *Controller) Pause(now int64) error {
	c.lock()
	defer c.unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if c.state != Running {
		return ErrInvalidState
	}
	c.commitClock(now)
	c.state = Paused
	c.pauseAt = now
	return nil
}

// Resume 仅 Paused 可调用。
func (c *Controller) Resume(now int64) error {
	c.lock()
	defer c.unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if c.state != Paused {
		return ErrInvalidState
	}
	c.commitClock(now)
	c.paused += now - c.pauseAt
	c.state = Running
	return nil
}

// Tick 在 Running 时推进到期阶段，返回本次发生的转移。
func (c *Controller) Tick(now int64) ([]Transition, error) {
	c.lock()
	defer c.unlock()
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	if c.state != Running {
		// Tick 在非 Running 状态下不报错、无转移，但仍是一次被接受的操作，登记最大时刻。
		c.commitClock(now)
		return nil, nil
	}
	c.commitClock(now)
	var transitions []Transition
	for c.level < len(c.steps)-1 {
		due := c.enteredAt + c.steps[c.level].HoldMs + c.paused
		if now < due {
			break
		}
		next := c.level + 1
		transitions = append(transitions, Transition{From: c.level, To: next, AtMs: due})
		c.level = next
		c.enteredAt = due
		c.paused = 0
	}
	if c.level == len(c.steps)-1 {
		c.state = Completed
	}
	return transitions, nil
}

// Rollback 可在 Running、Paused、Completed 调用。
func (c *Controller) Rollback(now int64) error {
	c.lock()
	defer c.unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if c.state != Running && c.state != Paused && c.state != Completed {
		return ErrInvalidState
	}
	c.commitClock(now)
	c.state = RolledBack
	c.level = -1
	c.enteredAt = 0
	c.paused = 0
	c.pauseAt = 0
	return nil
}

// Status 返回状态、级别与当前百分比。
func (c *Controller) Status() StatusSnapshot {
	c.lock()
	defer c.unlock()
	snapshot := StatusSnapshot{State: c.state, Step: -1, Percent: 0}
	if c.level >= 0 {
		snapshot.Step = c.level
		snapshot.Percent = c.steps[c.level].Percent
	}
	return snapshot
}

// InRollout 按确定性 FNV-1a 分桶判定用户是否在发布范围内。
func (c *Controller) InRollout(user string) (bool, error) {
	if user == "" {
		return false, ErrEmptyUser
	}
	c.lock()
	percent := 0
	if c.level >= 0 {
		percent = c.steps[c.level].Percent
	}
	c.unlock()

	var hash uint32 = 2166136261
	buf := make([]byte, 0, len(c.salt)+1+len(user))
	buf = append(buf, c.salt...)
	buf = append(buf, 0)
	buf = append(buf, user...)
	for _, b := range buf {
		hash ^= uint32(b)
		hash *= 16777619
	}
	bucket := hash % 10000
	return int(bucket) < percent*100, nil
}

func (c *Controller) lock() {
	c.mu <- struct{}{}
}

func (c *Controller) unlock() {
	<-c.mu
}

// checkClock 必须在持锁且其他状态检查之前调用；它只校验不提交。
func (c *Controller) checkClock(now int64) error {
	if c.haveNow && now < c.maxNow {
		return ErrClockRewind
	}
	return nil
}

// commitClock 在状态检查通过、操作整体被接受之后调用。
func (c *Controller) commitClock(now int64) {
	c.maxNow = now
	c.haveNow = true
}
