package exam

import (
	"fmt"
	"sync"
)

// Engine 为在线考试会话引擎。所有操作可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
// 时间为服务端单调不减的整数时刻，由调用方传入。
type Engine struct {
	mu       sync.Mutex
	cfg      Config
	now      int64
	sessions map[string]*session
	tokenSeq int64
}

// NewEngine 校验配置并创建引擎。
func NewEngine(cfg Config) (*Engine, error) {
	switch {
	case cfg.AnswerBudget <= 0:
		return nil, fmt.Errorf("%w: AnswerBudget must be positive", ErrInvalidParam)
	case cfg.PauseBudget < 0:
		return nil, fmt.Errorf("%w: PauseBudget must be non-negative", ErrInvalidParam)
	case cfg.SinglePauseMax < 0:
		return nil, fmt.Errorf("%w: SinglePauseMax must be non-negative", ErrInvalidParam)
	case cfg.Window < 0:
		return nil, fmt.Errorf("%w: Window must be non-negative", ErrInvalidParam)
	case cfg.WarnThreshold < 1 || cfg.LockThreshold < cfg.WarnThreshold:
		return nil, fmt.Errorf("%w: require 1 <= WarnThreshold <= LockThreshold", ErrInvalidParam)
	case cfg.WarnLimit < 1:
		return nil, fmt.Errorf("%w: WarnLimit must be positive", ErrInvalidParam)
	}
	return &Engine{cfg: cfg, sessions: make(map[string]*session)}, nil
}

// tick 校验并推进服务端时钟。被拒绝的操作不会推进任何会话时钟。
func (e *Engine) tick(now int64) error {
	if now < e.now {
		return ErrClockRegression
	}
	e.now = now
	return nil
}

// touch 定位会话并惰性落地自动结束。
func (e *Engine) touch(id string) (*session, error) {
	s := e.sessions[id]
	if s == nil {
		return nil, ErrSessionNotFound
	}
	s.maybeEnd(e.now, &e.cfg)
	return s, nil
}

// StartSession 开启会话，deadline 为绝对截止时刻，返回初始代次。
func (e *Engine) StartSession(id string, now, deadline int64) (int64, error) {
	if id == "" || deadline <= now {
		return 0, fmt.Errorf("%w: empty id or deadline not after now", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return 0, err
	}
	if e.sessions[id] != nil {
		return 0, ErrSessionExists
	}
	s := newSession(id, now, deadline, e.cfg.AnswerBudget)
	e.sessions[id] = s
	return s.generation, nil
}

// SubmitAnswer 接收一条带序号的作答记录。
func (e *Engine) SubmitAnswer(id string, now int64, a Answer) error {
	if id == "" || a.QuestionID == "" || a.Seq < 0 || a.Generation <= 0 {
		return fmt.Errorf("%w: bad answer fields", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return err
	}
	s, err := e.touch(id)
	if err != nil {
		return err
	}
	if s.status == StatusEnded {
		return ErrSessionEnded
	}
	if a.Generation != s.generation {
		return ErrStaleGeneration
	}
	switch s.status {
	case StatusPaused:
		// 暂停前已发出（序号小于暂停水位）的作答仍被接受。
		if a.Seq >= s.waterline {
			return ErrAnswerWhilePaused
		}
	case StatusLocked:
		return ErrAnswerWhileLocked
	}
	return s.land(a)
}

// RecordEvent 上报一条异常事件，按服务端到达时刻计入滑动窗口。
func (e *Engine) RecordEvent(id string, now int64, kind EventKind, clientTime int64) error {
	if id == "" || (kind != EventLeavePage && kind != EventSwitchWindow) {
		return fmt.Errorf("%w: bad event fields", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return err
	}
	s, err := e.touch(id)
	if err != nil {
		return err
	}
	if s.status == StatusEnded {
		return ErrSessionEnded
	}
	s.addEvent(now, kind, clientTime, &e.cfg)
	return nil
}

// Disconnect 上报断线，会话转为暂停并签发续考凭证。
func (e *Engine) Disconnect(id string, now int64) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%w: empty id", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return "", err
	}
	s, err := e.touch(id)
	if err != nil {
		return "", err
	}
	if s.status == StatusEnded {
		return "", ErrSessionEnded
	}
	if s.status != StatusActive {
		return "", ErrNotActive
	}
	e.tokenSeq++
	token := fmt.Sprintf("resume-%s-%d", id, e.tokenSeq)
	s.pause(now, token)
	return token, nil
}

// Resume 凭最近一次暂停签发的凭证续考，返回新的会话代次。
func (e *Engine) Resume(id, token string, now int64) (int64, error) {
	if id == "" || token == "" {
		return 0, fmt.Errorf("%w: empty id or token", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return 0, err
	}
	s, err := e.touch(id)
	if err != nil {
		return 0, err
	}
	if s.status == StatusEnded {
		return 0, ErrSessionEnded
	}
	if token != s.token {
		return 0, ErrInvalidToken
	}
	if s.status != StatusPaused {
		return 0, ErrNotPaused
	}
	s.resume(now)
	return s.generation, nil
}

// Submit 考生主动提交并结算。重复提交被拒绝且可区分。
func (e *Engine) Submit(id string, now int64) (Settlement, error) {
	if id == "" {
		return Settlement{}, fmt.Errorf("%w: empty id", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return Settlement{}, err
	}
	s, err := e.touch(id)
	if err != nil {
		return Settlement{}, err
	}
	if s.settlement != nil {
		return Settlement{}, ErrAlreadySettled
	}
	s.settle(now, EndReasonSubmitted, false, &e.cfg)
	return *s.settlement, nil
}

// Extend 监考延长作答预算，不改变绝对截止时刻；仅对尚未结束的会话有效。
func (e *Engine) Extend(id string, now, extra int64) error {
	if id == "" || extra <= 0 {
		return fmt.Errorf("%w: empty id or non-positive extra", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return err
	}
	s, err := e.touch(id)
	if err != nil {
		return err
	}
	if s.status == StatusEnded {
		return ErrSessionEnded
	}
	s.budget += extra
	return nil
}

// Unlock 监考解除锁定，不清空窗口内事件。
func (e *Engine) Unlock(id string, now int64) error {
	if id == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return err
	}
	s, err := e.touch(id)
	if err != nil {
		return err
	}
	if s.status == StatusEnded {
		return ErrSessionEnded
	}
	if s.status != StatusLocked {
		return ErrNotLocked
	}
	s.unlock()
	return nil
}

// SettlementOf 查询会话结算结果；未结束时 ok 为 false。
// 该查询同样会惰性落地自动结束。
func (e *Engine) SettlementOf(id string, now int64) (set Settlement, ok bool, err error) {
	if id == "" {
		return Settlement{}, false, fmt.Errorf("%w: empty id", ErrInvalidParam)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.tick(now); err != nil {
		return Settlement{}, false, err
	}
	s, err := e.touch(id)
	if err != nil {
		return Settlement{}, false, err
	}
	if s.settlement == nil {
		return Settlement{}, false, nil
	}
	return *s.settlement, true, nil
}
