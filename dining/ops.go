package dining

import (
	"errors"
	"fmt"
)

// reject 记录一次拒绝日志；拒绝不得改变任何进程、叉或令牌。
func (c *Coordinator) reject(op, input, reason string) error {
	c.emit(op, input, "rejected: state unchanged", false, reason)
	return errors.New(reason)
}

func (c *Coordinator) BecomeHungry(p int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	input := fmt.Sprintf("p%d -> hungry", p)
	s, ok := c.procs[p]
	if !ok {
		return c.reject("BecomeHungry", input, ErrNoSuchProcess.Error())
	}
	if s != Thinking {
		// 饥饿/进餐中重复变饥饿按当前状态区分拒绝。
		if s == Eating {
			return c.reject("BecomeHungry", input, ErrNotEating.Error()+
				" (process is eating)")
		}
		return c.reject("BecomeHungry", input, ErrNotHungry.Error()+
			" (process is already hungry)")
	}
	c.procs[p] = Hungry

	// 对每把缺少且自己持有令牌的叉：把令牌发给对方作为请求。
	var requested, missing []int
	for _, e := range c.incident(p) {
		switch {
		case holdsFork(e, p):
			// 已持有，等待即可。
		case holdsToken(e, p):
			c.sendRequest(e, p)
			requested = append(requested, other(e, p))
		default:
			// 叉、令牌都不在手：此前已发出请求，等待净叉到达。
			missing = append(missing, other(e, p))
		}
	}
	c.emit("BecomeHungry", input,
		fmt.Sprintf("p%d is hungry; requests=%v pending=%v", p, requested, missing),
		true, "thinking->hungry; missing forks with local token are requested")
	return nil
}

func (c *Coordinator) StartEating(p int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	input := fmt.Sprintf("p%d -> eating", p)
	s, ok := c.procs[p]
	if !ok {
		return c.reject("StartEating", input, ErrNoSuchProcess.Error())
	}
	if s == Thinking {
		return c.reject("StartEating", input, ErrNotHungry.Error())
	}
	if s == Eating {
		return c.reject("StartEating", input, ErrNotHungry.Error()+
			" (already eating)")
	}
	for _, e := range c.incident(p) {
		if !holdsFork(e, p) {
			return c.reject("StartEating", input, ErrMissingFork.Error())
		}
	}
	c.procs[p] = Eating
	c.emit("StartEating", input, fmt.Sprintf("p%d is eating", p), true,
		"hungry and holds every incident fork: exclusive access granted")
	return nil
}

func (c *Coordinator) FinishEating(p int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	input := fmt.Sprintf("p%d -> thinking", p)
	s, ok := c.procs[p]
	if !ok {
		return c.reject("FinishEating", input, ErrNoSuchProcess.Error())
	}
	if s != Eating {
		return c.reject("FinishEating", input, ErrNotEating.Error())
	}

	// 进餐结束：手中所有叉变脏；并立即满足暂存的请求。
	c.procs[p] = Thinking
	var granted []int
	for _, e := range c.incident(p) {
		if holdsFork(e, p) {
			e.dirty = true
		}
		if e.deferred(p) {
			e.setDeferred(p, false)
			if holdsFork(e, p) {
				// 洗净发出；令牌此前随暂存请求到达，已在自己手里。
				c.sendCleanFork(e, p)
				granted = append(granted, other(e, p))
			}
		}
	}
	c.emit("FinishEating", input,
		fmt.Sprintf("p%d is thinking; clean forks granted to %v", p, granted), true,
		"forks become dirty; deferred requests satisfied immediately with clean forks")
	return nil
}

func (c *Coordinator) BecomeThinking(p int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	input := fmt.Sprintf("p%d -> thinking (explicit)", p)
	s, ok := c.procs[p]
	if !ok {
		return c.reject("BecomeThinking", input, ErrNoSuchProcess.Error())
	}
	switch s {
	case Hungry:
		return c.reject("BecomeThinking", input, ErrHungryToThink.Error())
	case Eating:
		return c.reject("BecomeThinking", input, ErrEatingToThink.Error())
	default:
		return c.reject("BecomeThinking", input, ErrAlreadyThinking.Error())
	}
}

func (c *Coordinator) State(p int) (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.procs[p]
	if !ok {
		return Thinking, ErrNoSuchProcess
	}
	return s, nil
}

// Deliver 经注入网络投递 from->to 信道队首消息。
// 拒绝投递不得改变任何进程、叉、令牌或网络队列。
func (c *Coordinator) Deliver(d Delivery) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	input := fmt.Sprintf("deliver %s p%d->p%d on edge%v",
		d.Msg.Type, d.From, d.To, d.Msg.Edge)
	if _, ok := c.procs[d.From]; !ok {
		return c.reject("Deliver", input, ErrNoSuchProcess.Error())
	}
	if _, ok := c.procs[d.To]; !ok {
		return c.reject("Deliver", input, ErrNoSuchProcess.Error())
	}
	if d.From == d.To {
		return c.reject("Deliver", input, ErrSelfLoop.Error())
	}
	key, _ := canonEdge(d.Msg.Edge[0], d.Msg.Edge[1])
	e, ok := c.edges[key]
	if !ok {
		return c.reject("Deliver", input, ErrNoSuchMessage.Error()+
			" (no such edge)")
	}
	if d.Msg.From != d.From || (d.To != e.a && d.To != e.b) ||
		(d.From != e.a && d.From != e.b) {
		return c.reject("Deliver", input, ErrNoSuchMessage.Error()+
			" (message does not match channel)")
	}

	// 必须是该有向信道真正的队首消息；不存在或乱序投递都拒绝。
	head, exists := c.net.Peek(d.From, d.To)
	if !exists || head != d.Msg {
		return c.reject("Deliver", input, ErrNoSuchMessage.Error())
	}

	switch d.Msg.Type {
	case Request:
		if e.tokenAt != -1 {
			return c.reject("Deliver", input, ErrNoSuchMessage.Error()+
				" (token already present)")
		}
	case Fork:
		if e.forkAt != -1 {
			return c.reject("Deliver", input, ErrNoSuchMessage.Error()+
				" (fork already present)")
		}
	}

	// 校验通过，正式出队并处理；此后所有变更都在锁内原子完成。
	c.net.Pop(d.From, d.To)
	switch d.Msg.Type {
	case Request:
		c.deliverRequest(e, d.From, d.To)
	case Fork:
		c.deliverFork(e, d.From, d.To)
	}
	return nil
}

// deliverRequest 处理 to 收到 from 的请求（令牌随消息到达 to）。
func (c *Coordinator) deliverRequest(e *edgeState, from, to int) {
	input := fmt.Sprintf("request p%d->p%d edge(%d-%d)", from, to, e.a, e.b)
	e.tokenAt = to

	holder := c.procs[to]
	switch {
	case holdsFork(e, to) && e.dirty && holder != Eating:
		// 手中叉为脏且自己未在进餐：洗净发出。
		c.sendCleanFork(e, to)
		reason := "dirty fork held and not eating: wash clean and send"
		if holder == Hungry {
			// 自己仍饥饿：立即用这枚令牌再请求。
			c.sendRequest(e, to)
			reason += "; still hungry: re-request with the token immediately"
		}
		c.emit("Deliver(request)", input, edgeString(e), true, reason)
	case holder == Eating || (holdsFork(e, to) && !e.dirty):
		// 叉为净，或自己正在进餐：暂存请求。
		// 暂存位记在持有者（接收方）一侧：进餐结束后由持有者检查并满足。
		e.setDeferred(to, true)
		c.emit("Deliver(request)", input, edgeString(e), true,
			"fork clean or holder eating: request deferred until eating ends")
	default:
		// 叉在途（对端持有）：令牌留在此端，等待净叉到达。
		c.emit("Deliver(request)", input, edgeString(e), true,
			"fork in transit: token retained until clean fork arrives")
	}
}

// deliverFork 处理 to 收到 from 的叉；收到的叉恒为净。
func (c *Coordinator) deliverFork(e *edgeState, from, to int) {
	input := fmt.Sprintf("fork p%d->p%d edge(%d-%d)", from, to, e.a, e.b)
	e.forkAt = to
	e.dirty = false

	// 自己仍饥饿、且这枚叉此前是应自己请求而到：若令牌恰在手中（净叉与
	// 请求交错的边界情形），不做额外动作；令牌唯一性已保证不会双请求。
	c.emit("Deliver(fork)", input, edgeString(e), true,
		"clean fork received; holder may eat once all forks are held")
}
