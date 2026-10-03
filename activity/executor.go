package activity

import (
	"io"
	"log"
	"os"
	"sync"
)

// Executor 管理全局时钟与全部活动。
type Executor struct {
	mu    sync.Mutex
	clock int64
	items map[string]*activity
	log   *log.Logger
}

// New 创建执行器，全局时钟为 0。
func New(logger *log.Logger) *Executor {
	if logger == nil {
		logger = log.New(os.Stderr, "activity ", log.LstdFlags|log.Lmicroseconds)
	}
	return &Executor{clock: 0, items: map[string]*activity{}, log: logger}
}

// NewDiscard 创建不输出日志的执行器（测试用）。
func NewDiscard() *Executor {
	return New(log.New(io.Discard, "", 0))
}

// with 在虚拟副本上推演后执行操作；被拒绝时不改任何状态。
func (e *Executor) with(id []byte, now int64, fn func(a *activity) error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(id) == 0 || !validNow(now) {
		return ErrArgument
	}
	if now < e.clock {
		return ErrClock
	}
	a, ok := e.items[string(id)]
	if !ok {
		return ErrNotFound
	}
	cp := a.clone()
	cp.advance(now)
	if err := fn(cp); err != nil {
		e.log.Printf("op id=%q now=%d -> %v", string(id), now, err)
		return err
	}
	e.items[string(id)] = cp
	e.clock = now
	return nil
}

// Schedule 创建活动并完成首次排队。
func (e *Executor) Schedule(id []byte, cfg Config, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(id) == 0 || !validNow(now) {
		return ErrArgument
	}
	dc := cfg.deadlineConfig()
	if err := dc.Validate(); err != nil {
		return ErrConfig
	}
	if err := cfg.policy().Validate(); err != nil {
		return ErrConfig
	}
	if now < e.clock {
		return ErrClock
	}
	sid := string(id)
	if _, ok := e.items[sid]; ok {
		return ErrExists
	}
	a := newActivity(sid, cfg, now, e.log)
	a.advance(now)
	e.items[sid] = a
	e.clock = now
	e.log.Printf("schedule id=%q now=%d cfg=%+v -> k=1 scheduled g=%d", sid, now, cfg, now)
	return nil
}

// Start 领取 Scheduled 活动，返回 (尝试号, 进度)。
func (e *Executor) Start(id []byte, now int64) (int, int64, error) {
	var k int
	var progress int64
	err := e.with(id, now, func(a *activity) error {
		if a.state != Scheduled {
			if a.state == StateTerminal {
				return ErrTerminal
			}
			return ErrState
		}
		a.start(now)
		k, progress = a.k, a.progress
		a.trace(now, "start running")
		return nil
	})
	if err == nil {
		e.log.Printf("start id=%q now=%d -> k=%d progress=%d", string(id), now, k, progress)
	}
	return k, progress, err
}

// Heartbeat 上报心跳与进度。
func (e *Executor) Heartbeat(id []byte, k int, now, progress int64) error {
	if !validK(k) || !validNow(now) {
		return ErrArgument
	}
	return e.with(id, now, func(a *activity) error {
		if a.k != k {
			return ErrStale
		}
		if a.state == StateTerminal {
			return ErrTerminal
		}
		if a.state != Running {
			return ErrState
		}
		a.heartbeat(now, progress)
		a.trace(now, "heartbeat progress="+itoa(progress))
		return nil
	})
}

// Complete 上报尝试成功。
func (e *Executor) Complete(id []byte, k int, now int64) error {
	if !validK(k) || !validNow(now) {
		return ErrArgument
	}
	return e.with(id, now, func(a *activity) error {
		if a.k != k {
			return ErrStale
		}
		if a.state == StateTerminal {
			return ErrTerminal
		}
		if a.state != Running {
			return ErrState
		}
		a.finish(TermCompleted, None, now)
		a.trace(now, "complete")
		return nil
	})
}

// Fail 上报应用失败。
func (e *Executor) Fail(id []byte, k int, now int64, retryable bool) error {
	if !validK(k) || !validNow(now) {
		return ErrArgument
	}
	return e.with(id, now, func(a *activity) error {
		if a.k != k {
			return ErrStale
		}
		if a.state == StateTerminal {
			return ErrTerminal
		}
		if a.state != Running {
			return ErrState
		}
		if !retryable {
			a.finish(TermFailed, App, now)
			a.trace(now, "app failure non-retryable -> terminal failed(app)")
			return nil
		}
		a.failAttempt(App, now)
		return nil
	})
}

// Status 返回只读状态视图，不改变任何内部状态。
func (e *Executor) Status(id []byte, now int64) (Status, error) {
	if len(id) == 0 || !validNow(now) {
		return Status{}, ErrArgument
	}
	if now < e.clock {
		return Status{}, ErrClock
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.items[string(id)]
	if !ok {
		return Status{}, ErrNotFound
	}
	cp := a.clone()
	cp.advance(now)
	st := cp.snapshot()
	e.log.Printf("status id=%q now=%d -> %+v probe=%d", string(id), now, st, cp.probe)
	return st, nil
}
