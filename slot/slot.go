// Package slot 维护全局执行位与 FIFO 等待队列。
package slot

// Pool 是执行位与等待队列的容器。
type Pool struct {
	capacity int
	busy     int // Running + Cancelling 占用的执行位数
	head     *node
	tail     *node
	nodes    map[int]*node // 队列中运行 ID -> 节点，支持 O(1) 任意移除
}

type node struct {
	id   int
	prev *node
	next *node
}

// New 创建容量为 C 的执行位池。
func New(C int) *Pool {
	return &Pool{capacity: C, nodes: make(map[int]*node)}
}

// Busy 返回已占用的执行位数（Running + Cancelling）。
func (p *Pool) Busy() int { return p.busy }

// Free 返回空闲执行位数。
func (p *Pool) Free() int { return p.capacity - p.busy }

// HasFree 报告是否还有空闲执行位。
func (p *Pool) HasFree() bool { return p.busy < p.capacity }

// Acquire 占用一个执行位。
func (p *Pool) Acquire() { p.busy++ }

// Release 释放一个执行位。
func (p *Pool) Release() { p.busy-- }

// Len 返回等待队列长度。
func (p *Pool) Len() int { return len(p.nodes) }

// Push 把运行加入等待队列尾。
func (p *Pool) Push(id int) {
	n := &node{id: id, prev: p.tail}
	if p.tail != nil {
		p.tail.next = n
	} else {
		p.head = n
	}
	p.tail = n
	p.nodes[id] = n
}

// Front 返回队首运行 ID；队列为空时第二个返回值为 false。
func (p *Pool) Front() (int, bool) {
	if p.head == nil {
		return 0, false
	}
	return p.head.id, true
}

// Pop 移除并返回队首运行 ID。
func (p *Pool) Pop() (int, bool) {
	if p.head == nil {
		return 0, false
	}
	n := p.head
	p.head = n.next
	if p.head != nil {
		p.head.prev = nil
	} else {
		p.tail = nil
	}
	delete(p.nodes, n.id)
	return n.id, true
}

// Remove O(1) 地从队列任意位置移除指定运行；不存在则返回 false。
func (p *Pool) Remove(id int) bool {
	n := p.nodes[id]
	if n == nil {
		return false
	}
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		p.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		p.tail = n.prev
	}
	delete(p.nodes, id)
	return true
}

// IDs 按队首到队尾返回队列内容（供测试与日志使用）。
func (p *Pool) IDs() []int {
	out := make([]int, 0, len(p.nodes))
	for n := p.head; n != nil; n = n.next {
		out = append(out, n.id)
	}
	return out
}
