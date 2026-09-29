package dining

import (
	"fmt"
	"sort"
)

// canonEdge 把边规范化为小端在前；返回第三个值表示是否为自环输入。
func canonEdge(x, y int) ([2]int, bool) {
	if x == y {
		return [2]int{x, y}, true
	}
	if x > y {
		x, y = y, x
	}
	return [2]int{x, y}, false
}

// other 返回叉/令牌在边两端意义下的对端。
func other(e *edgeState, p int) int {
	if e.a == p {
		return e.b
	}
	return e.a
}

// holdsFork / holdsToken 是边状态上的小谓词。
func holdsFork(e *edgeState, p int) bool  { return e.forkAt == p }
func holdsToken(e *edgeState, p int) bool { return e.tokenAt == p }

// setDeferred / deferred 把“来自 p 的暂存请求”投影到 deferA/deferB。
func (e *edgeState) deferred(p int) bool {
	if p == e.a {
		return e.deferA
	}
	return e.deferB
}

func (e *edgeState) setDeferred(p int, v bool) {
	if p == e.a {
		e.deferA = v
	} else {
		e.deferB = v
	}
}

// incident 返回进程 p 的全部邻边。
func (c *Coordinator) incident(p int) []*edgeState {
	var out []*edgeState
	for k, e := range c.edges {
		if k[0] == p || k[1] == p {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].a != out[j].a {
			return out[i].a < out[j].a
		}
		return out[i].b < out[j].b
	})
	return out
}

func (c *Coordinator) emit(op, input, output string, accepted bool, reason string) {
	c.seq++
	if c.log == nil {
		return
	}
	c.log.Log(Event{
		Seq:      c.seq,
		Op:       op,
		Input:    input,
		Output:   output,
		Accepted: accepted,
		Reason:   reason,
	})
}

// sendRequest 发送 p 在边 e 上持有的令牌，作为对对端的叉请求。
// 调用前必须保证 holdsToken(e,p)：令牌随请求离手、进入在途。
func (c *Coordinator) sendRequest(e *edgeState, p int) {
	q := other(e, p)
	e.tokenAt = -1
	c.net.Send(p, q, Message{Edge: [2]int{e.a, e.b}, From: p, Type: Request})
}

// sendCleanFork 洗净手中叉并发出（收到者必为净），令牌留在自己手里。
func (c *Coordinator) sendCleanFork(e *edgeState, p int) {
	q := other(e, p)
	e.dirty = false
	e.forkAt = -1
	c.net.Send(p, q, Message{Edge: [2]int{e.a, e.b}, From: p, Type: Fork})
}

func stateName(s State) string { return s.String() }

func edgeString(e *edgeState) string {
	holder := "in-transit"
	if e.forkAt != -1 {
		holder = fmt.Sprintf("p%d", e.forkAt)
	}
	token := "in-transit"
	if e.tokenAt != -1 {
		token = fmt.Sprintf("p%d", e.tokenAt)
	}
	clean := "clean"
	if e.dirty {
		clean = "dirty"
	}
	return fmt.Sprintf("edge(%d-%d){fork:%s/%s,token:%s,defer:[a=%v,b=%v]}",
		e.a, e.b, holder, clean, token, e.deferA, e.deferB)
}
