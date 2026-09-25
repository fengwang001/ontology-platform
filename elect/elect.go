// Package elect 在 ring 上模拟 Chang–Roberts 环形领导者选举。
// 依赖 ring，不依赖 api。
package elect

import (
	"errors"

	"ontology/ring"
)

// ErrNoTerminate 是终止性守卫：任何消息绕环超过一圈即报错（正常不会触发）。
var ErrNoTerminate = errors.New("elect: message lapped the ring")

// Event 是分步表中的一行：哪个节点收到哪个 ID、比较后的动作与去向。
type Event struct {
	At     string // 接收节点 ID
	Msg    string // 收到的 ID
	From   string // 发送方节点 ID
	Action string // discard / forward / declare
	To     string // forward 时的去向
}

// Result 是选举结果：胜者 ID、总消息数、分步轨迹。
type Result struct {
	Winner   string
	Messages int
	Trace    []Event
}

type msg struct {
	at, id, from string
	hops         int
}

// Elect 按规则模拟：每个节点先把自己 ID 发给后继；收到 ID 时
// 大则转发、小则丢弃、等则宣告。FIFO 处理，结果与调度无关。
func Elect(r *ring.Ring) (Result, error) {
	order := r.Order()
	n := len(order)
	if n == 0 {
		return Result{}, ring.ErrEmpty
	}
	succ := make(map[string]string, n)
	for i, id := range order {
		succ[id] = order[(i+1)%n]
	}
	var res Result
	queue := make([]msg, 0, 2*n)
	for _, id := range order { // ① 每个节点把自己的 ID 发给后继
		queue = append(queue, msg{at: succ[id], id: id, from: id, hops: 1})
		res.Messages++
	}
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if m.hops > n { // 每条消息至多绕环一圈
			return Result{}, ErrNoTerminate
		}
		ev := Event{At: m.at, Msg: m.id, From: m.from}
		switch {
		case m.id == m.at: // 收到 == 自身：宣告当选，选举结束
			ev.Action = "declare"
			res.Trace = append(res.Trace, ev)
			res.Winner = m.at
			return res, nil
		case m.id > m.at: // 收到 > 自身：转发给后继
			ev.Action, ev.To = "forward", succ[m.at]
			res.Messages++
			queue = append(queue, msg{at: succ[m.at], id: m.id, from: m.at, hops: m.hops + 1})
		default: // 收到 < 自身：丢弃
			ev.Action = "discard"
		}
		res.Trace = append(res.Trace, ev)
	}
	return Result{}, ErrNoTerminate
}
