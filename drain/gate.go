package drain

import "time"

// New 创建闸门。now 为可注入时钟，用于 Tick 与 deadline 判定；
// 若为 nil 则使用 time.Now。
func New(now func() time.Time) *Gate {
	if now == nil {
		now = time.Now
	}
	return &Gate{
		now:    now,
		zeroCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Enter 申请放行一个新请求。
//
// 停机开始后返回 (nil, ErrShuttingDown)，并将 Rejected 加一。
// 成功时返回的 release 必须且只需调用一次：重复调用（含并发）均为无操作，
// 计数绝不会变为负数。
func (g *Gate) Enter() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.shuttingDown {
		g.rejected++
		return nil, ErrShuttingDown
	}

	g.admitted++
	g.inFlight++

	var released atomicBool
	release := func() {
		g.releaseOnce(&released)
	}
	return release, nil
}

// releaseOnce 借助 CAS 语义保证同一个 release 至多生效一次。
func (g *Gate) releaseOnce(fired *atomicBool) {
	if !fired.cas(false, true) {
		return
	}

	g.mu.Lock()
	g.inFlight--
	if g.inFlight == 0 {
		close(g.zeroCh)
		g.zeroCh = make(chan struct{})
	}
	g.mu.Unlock()
}
