package retry

import (
	"errors"
	"sync"
)

// maxTime 为时间参数（created / now）的合法上界。
const maxTime = int64(1_000_000_000_000)

// Class 为失败类别。
type Class string

const (
	Transient Class = "transient"
	Throttled Class = "throttled"
	Permanent Class = "permanent"
)

func (c Class) valid() bool {
	return c == Transient || c == Throttled || c == Permanent
}

// DeadReason 为死信原因。
type DeadReason string

const (
	ReasonPermanent DeadReason = "permanent"
	ReasonThrottled DeadReason = "throttled"
	ReasonExhausted DeadReason = "exhausted"
	ReasonBudget    DeadReason = "budget"
	ReasonExpired   DeadReason = "expired"
)

// 拒绝原因，可用 errors.Is 判定。
var (
	ErrInvalidParam   = errors.New("retry: invalid parameter")
	ErrNotFound       = errors.New("retry: task not found")
	ErrFinished       = errors.New("retry: task already finished")
	ErrClockBackwards = errors.New("retry: clock backwards")
	ErrTooEarly       = errors.New("retry: too early")
	ErrDuplicate      = errors.New("retry: duplicate task id")
)

// JitterFunc 为注入的确定性抖动函数，返回 int64。
type JitterFunc func(id string, n, d int64) int64

// Config 为调度器构造参数。
type Config struct {
	Base  int64 // 退避基数，1 到 10^6
	Cap   int64 // 退避上限，Base 到 10^9
	M     int64 // 单渠道连续失败上限，1 到 16
	A     int64 // 总失败预算，1 到 100
	RAcap int64 // Retry-After 上限，0 到 10^9
	K     int64 // 渠道熔断阈值，1 到 1000
	Cool  int64 // 熔断时长，1 到 10^9
}

func (c Config) valid() bool {
	return c.Base >= 1 && c.Base <= 1_000_000 &&
		c.Cap >= c.Base && c.Cap <= 1_000_000_000 &&
		c.M >= 1 && c.M <= 16 &&
		c.A >= 1 && c.A <= 100 &&
		c.RAcap >= 0 && c.RAcap <= 1_000_000_000 &&
		c.K >= 1 && c.K <= 1000 &&
		c.Cool >= 1 && c.Cool <= 1_000_000_000
}

// Result 为 Fail 的回报：要么给出下次尝试的渠道与时刻，要么报告死信。
type Result struct {
	Channel string
	NextAt  int64
	Dead    bool
	Reason  DeadReason
}

type task struct {
	chain    []string
	deadline int64
	cur      int
	n        int64
	att      int64
	nextAt   int64
	done     bool
	success  bool
	reason   DeadReason
}

// Scheduler 为并发安全的重试调度器。
type Scheduler struct {
	mu        sync.Mutex
	cfg       Config
	jitter    JitterFunc
	tasks     map[string]*task
	gfail     map[string]int64
	openUntil map[string]int64
	maxNow    int64
}

// New 校验构造参数并创建调度器。
func New(cfg Config, jitter JitterFunc) (*Scheduler, error) {
	if !cfg.valid() || jitter == nil {
		return nil, ErrInvalidParam
	}
	return &Scheduler{
		cfg:       cfg,
		jitter:    jitter,
		tasks:     make(map[string]*task),
		gfail:     make(map[string]int64),
		openUntil: make(map[string]int64),
	}, nil
}

// Submit 登记任务：id 非空且未登记，chain 为 1 到 4 个互不相同的非空渠道名，
// created 在 [0, 1e12]，ttl 在 [1, 1e9]，deadline = created + ttl。
func (s *Scheduler) Submit(id string, chain []string, created, ttl int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || len(chain) < 1 || len(chain) > 4 ||
		created < 0 || created > maxTime || ttl < 1 || ttl > 1_000_000_000 {
		return ErrInvalidParam
	}
	seen := make(map[string]bool, len(chain))
	for _, ch := range chain {
		if ch == "" || seen[ch] {
			return ErrInvalidParam
		}
		seen[ch] = true
	}
	if _, ok := s.tasks[id]; ok {
		return ErrDuplicate
	}
	dup := make([]string, len(chain))
	copy(dup, chain)
	s.tasks[id] = &task{chain: dup, deadline: created + ttl, nextAt: created}
	return nil
}

// checkCommon 执行 Fail 与 Success 共用的拒绝链（不含参数校验）。
func (s *Scheduler) checkCommon(id string, now int64) (*task, error) {
	t, ok := s.tasks[id]
	if !ok {
		return nil, ErrNotFound
	}
	if t.done {
		return nil, ErrFinished
	}
	if now < s.maxNow {
		return nil, ErrClockBackwards
	}
	if now < t.nextAt {
		return nil, ErrTooEarly
	}
	return t, nil
}

// Fail 回报当前尝试失败，返回下次尝试的渠道与时刻，或死信原因。
func (s *Scheduler) Fail(id string, now int64, class Class, ra int64) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !class.valid() || ra < 0 || ra > 1_000_000_000 ||
		(class != Throttled && ra != 0) || now < 0 || now > maxTime {
		return Result{}, ErrInvalidParam
	}
	t, err := s.checkCommon(id, now)
	if err != nil {
		return Result{}, err
	}
	s.maxNow = now

	t.att++
	ch := t.chain[t.cur]
	if class != Permanent {
		s.gfail[ch]++
		if s.gfail[ch] >= s.cfg.K {
			s.openUntil[ch] = now + s.cfg.Cool
		}
	}

	var w int64
	var reason DeadReason
	switching := false
	switch {
	case class == Permanent:
		switching, reason = true, ReasonPermanent
	case class == Throttled && ra > s.cfg.RAcap:
		switching, reason = true, ReasonThrottled
	default:
		t.n++
		if t.n >= s.cfg.M {
			switching, reason = true, ReasonExhausted
		} else {
			d := s.cfg.Base << uint(t.n-1)
			if d > s.cfg.Cap {
				d = s.cfg.Cap
			}
			j := s.jitter(id, t.n, d)
			if j < 0 {
				j = 0
			}
			if hi := d / 4; j > hi {
				j = hi
			}
			w = d - j
			if ra > w {
				w = ra
			}
		}
	}

	if switching {
		next := -1
		for i := t.cur + 1; i < len(t.chain); i++ {
			if s.openUntil[t.chain[i]] <= now {
				next = i
				break
			}
		}
		if next < 0 {
			t.done, t.reason, t.n = true, reason, 0
			return Result{Dead: true, Reason: reason}, nil
		}
		t.cur, t.n, w = next, 0, 0
	}

	if t.att >= s.cfg.A {
		t.done, t.reason, t.n = true, ReasonBudget, 0
		return Result{Dead: true, Reason: ReasonBudget}, nil
	}

	t.nextAt = now + w
	if t.nextAt > t.deadline {
		t.done, t.reason, t.n = true, ReasonExpired, 0
		return Result{Dead: true, Reason: ReasonExpired}, nil
	}
	return Result{Channel: t.chain[t.cur], NextAt: t.nextAt}, nil
}

// Success 标记任务完成，并把当前渠道的 gfail 清零（不解除已有熔断）。
func (s *Scheduler) Success(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || now > maxTime {
		return ErrInvalidParam
	}
	t, err := s.checkCommon(id, now)
	if err != nil {
		return err
	}
	s.maxNow = now
	t.done, t.success = true, true
	s.gfail[t.chain[t.cur]] = 0
	return nil
}
