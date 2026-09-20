package drain

import "time"

// Shutdown 开始停机：此后所有 Enter 被拒绝，并阻塞等待在途请求归零，
// 或到达 deadline。幂等：多次（含并发）调用共享同一次等待与同一结果。
func (g *Gate) Shutdown(deadline time.Time) error {
	g.mu.Lock()
	if !g.shuttingDown {
		g.shuttingDown = true
		g.deadline = deadline
		g.hasDeadline = true

		// 调用时已经没有在途请求：立即成功结束，不多等。
		if g.inFlight == 0 {
			g.finishLocked(nil)
		} else {
			go g.waitWatcher(deadline)
		}
	}
	ch := g.doneCh
	g.mu.Unlock()

	<-ch

	g.mu.Lock()
	err := g.result
	g.mu.Unlock()
	return err
}

// waitWatcher 是停机期间唯一的等待 goroutine：
// 在途归零立即成功结束；到达 deadline 仍有在途则以超时结束。
func (g *Gate) waitWatcher(deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	for {
		g.mu.Lock()
		zero := g.zeroCh
		done := g.doneCh
		if g.inFlight == 0 {
			g.finishLocked(nil)
			g.mu.Unlock()
			return
		}
		g.mu.Unlock()

		select {
		case <-done:
			// 已被 Tick 判定超时结束。
			return
		case <-zero:
			// 可能归零；循环开头在锁内复核，避免误判。
			continue
		case <-timer.C:
			g.mu.Lock()
			if g.inFlight > 0 {
				g.finishLocked(ErrDrainTimeout)
				g.mu.Unlock()
				return
			}
			g.mu.Unlock()
			// 归零与计时同时发生：下一轮锁内复核将成功结束。
		}
	}
}

// finishLocked 在持锁状态下结束停机流程，全程只执行一次。
func (g *Gate) finishLocked(err error) {
	if g.done {
		return
	}
	g.done = true
	g.result = err
	close(g.doneCh)
}

// Tick 用注入时钟重新判定是否已超时；若已过 deadline 且仍有在途，
// 则立即以超时结束停机。
func (g *Gate) Tick() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.shuttingDown || g.done {
		return
	}
	if g.hasDeadline && !g.now().Before(g.deadline) && g.inFlight > 0 {
		g.finishLocked(ErrDrainTimeout)
	}
}

// Stats 返回当前计数的一致性快照。
func (g *Gate) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Stats{
		Admitted: g.admitted,
		Rejected: g.rejected,
		InFlight: g.inFlight,
		Done:     g.done,
	}
}
