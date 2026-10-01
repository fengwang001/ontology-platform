// Package rollout 提供带注入时刻的特性渐进发布控制器。
package rollout

import (
	"errors"
	"sync"
)

// State 表示控制器生命周期状态。
type State int

const (
	Idle State = iota
	Running
	Paused
	Completed
	RolledBack
)

func (s State) String() string {
	switch s {
	case Idle:
		return "Idle"
	case Running:
		return "Running"
	case Paused:
		return "Paused"
	case Completed:
		return "Completed"
	case RolledBack:
		return "RolledBack"
	}
	return "Unknown"
}

// 可区分的错误原因。
var (
	ErrEmptySalt            = errors.New("rollout: salt 不能为空")
	ErrTooFewSteps          = errors.New("rollout: 阶梯至少需要 2 级")
	ErrPercentOutOfRange    = errors.New("rollout: 百分比必须是 1 到 100 的整数")
	ErrPercentNotIncreasing = errors.New("rollout: 百分比必须严格递增")
	ErrNegativeHold         = errors.New("rollout: Hold 不能为负")
	ErrClockBackwards       = errors.New("rollout: now 小于此前已接受操作的最大 now")
	ErrInvalidState         = errors.New("rollout: 当前状态不允许该操作")
	ErrEmptyUser            = errors.New("rollout: 用户不能为空")
)

// Step 为阶梯的一级。最后一级的 Hold 被忽略（但仍须不为负）。
type Step struct {
	Percent int
	Hold    int64
}

// Transition 记录一次阶段转移：来源级、目标级与转移时刻（取目标级生效的 due）。
type Transition struct {
	From int
	To   int
	At   int64
}

// Controller 是渐进发布控制器，所有方法可并发调用。
type Controller struct {
	mu         sync.Mutex
	salt       string
	steps      []Step
	state      State
	level      int
	enteredAt  int64
	paused     int64
	pauseStart int64
	maxNow     int64
	hasNow     bool
}

// New 构造控制器，按顺序校验：盐非空、阶梯至少 2 级、百分比在 [1,100]、
// 百分比严格递增、Hold 非负（含最后一级），只报第一个错误。
func New(salt string, steps []Step) (*Controller, error) {
	if salt == "" {
		return nil, ErrEmptySalt
	}
	if len(steps) < 2 {
		return nil, ErrTooFewSteps
	}
	for _, st := range steps {
		if st.Percent < 1 || st.Percent > 100 {
			return nil, ErrPercentOutOfRange
		}
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].Percent <= steps[i-1].Percent {
			return nil, ErrPercentNotIncreasing
		}
	}
	for _, st := range steps {
		if st.Hold < 0 {
			return nil, ErrNegativeHold
		}
	}
	copied := make([]Step, len(steps))
	copy(copied, steps)
	return &Controller{salt: salt, steps: copied, state: Idle, level: -1}, nil
}

// checkClock 校验时钟回拨；通过后记录 maxNow。被拒绝的调用不改变 maxNow。
func (c *Controller) checkClock(now int64) error {
	if c.hasNow && now < c.maxNow {
		return ErrClockBackwards
	}
	c.maxNow = now
	c.hasNow = true
	return nil
}

// Start 仅 Idle 或 RolledBack 可调用，进入第 0 级。
func (c *Controller) Start(now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if c.state != Idle && c.state != RolledBack {
		return ErrInvalidState
	}
	c.state = Running
	c.level = 0
	c.enteredAt = now
	c.paused = 0
	return nil
}

// Pause 仅 Running 可调用，记下暂停起点，不推进阶段。
func (c *Controller) Pause(now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if c.state != Running {
		return ErrInvalidState
	}
	c.state = Paused
	c.pauseStart = now
	return nil
}

// Resume 仅 Paused 可调用，把暂停时长累加进当前级，不推进阶段。
func (c *Controller) Resume(now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if c.state != Paused {
		return ErrInvalidState
	}
	c.paused += now - c.pauseStart
	c.state = Running
	return nil
}

// Tick 仅在 Running 时推进阶段，可在一次调用内连续跨越多级；
// 其他状态下无转移。返回本次发生的转移列表。
func (c *Controller) Tick(now int64) ([]Transition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	var transitions []Transition
	if c.state != Running {
		return transitions, nil
	}
	last := len(c.steps) - 1
	for c.level < last {
		due := c.enteredAt + c.steps[c.level].Hold + c.paused
		if now < due {
			break
		}
		transitions = append(transitions, Transition{From: c.level, To: c.level + 1, At: due})
		c.level++
		c.enteredAt = due
		c.paused = 0
	}
	if c.level == last {
		c.state = Completed
	}
	return transitions, nil
}

// Rollback 可在 Running、Paused、Completed 调用，状态变为 RolledBack、百分比为 0。
func (c *Controller) Rollback(now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if c.state != Running && c.state != Paused && c.state != Completed {
		return ErrInvalidState
	}
	c.state = RolledBack
	c.level = -1
	c.paused = 0
	return nil
}

// Status 返回状态、级别（Idle 与 RolledBack 为 -1）与当前百分比
// （Idle 与 RolledBack 为 0，Paused 沿用当前级百分比）。
func (c *Controller) Status() (State, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == Idle || c.state == RolledBack {
		return c.state, -1, 0
	}
	return c.state, c.level, c.steps[c.level].Percent
}

// bucket 对 salt + "\x00" + user 做 32 位 FNV-1a，再对 10000 取余。
func (c *Controller) bucket(user string) uint32 {
	const (
		offsetBasis uint32 = 2166136261
		prime       uint32 = 16777619
	)
	h := offsetBasis
	mix := func(b byte) {
		h ^= uint32(b)
		h *= prime
	}
	for i := 0; i < len(c.salt); i++ {
		mix(c.salt[i])
	}
	mix(0)
	for i := 0; i < len(user); i++ {
		mix(user[i])
	}
	return h % 10000
}

// InRollout 判定用户是否落入当前发布范围：b < 当前百分比*100。
func (c *Controller) InRollout(user string) (bool, error) {
	if user == "" {
		return false, ErrEmptyUser
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _, percent := c.statusLocked()
	return c.bucket(user) < uint32(percent)*100, nil
}

func (c *Controller) statusLocked() (State, int, int) {
	if c.state == Idle || c.state == RolledBack {
		return c.state, -1, 0
	}
	return c.state, c.level, c.steps[c.level].Percent
}
