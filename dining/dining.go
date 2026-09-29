// Package dining 实现基于脏/净叉（Chandy–Misra）的分布式资源冲突协调器。
//
// 冲突图中的每条无向边对应一份共享资源：恰好一把叉与一枚请求令牌。
// 相邻进程通过收发叉与请求令牌轮流独占资源，保证相邻进程从不同时进餐、
// 由叉的位置与脏净导出的优先关系始终无环，且没有进程饿死。
package dining

import (
	"errors"
	"fmt"
	"sync"
)

// State 是进程的三态状态机。
type State int

const (
	Thinking State = iota // 思考
	Hungry                // 饥饿
	Eating                // 进餐
)

func (s State) String() string {
	switch s {
	case Thinking:
		return "thinking"
	case Hungry:
		return "hungry"
	case Eating:
		return "eating"
	default:
		return "unknown"
	}
}

// MsgType 标识有向信道上消息的种类。
type MsgType int

const (
	// Request 是请求令牌消息：它在信道上的物理载体就是该边唯一的令牌。
	Request MsgType = iota
	// Fork 是叉消息：叉在途；收到的叉恒为净。
	Fork
)

func (m MsgType) String() string {
	switch m {
	case Request:
		return "request"
	case Fork:
		return "fork"
	default:
		return "unknown"
	}
}

// Message 是信道上承载的消息。
type Message struct {
	Edge [2]int // 规范化的冲突边（小端在前）
	From int    // 发送方
	Type MsgType
}

// Delivery 表示一次待注入的投递：从 From 到 To 的有向信道上的一条消息。
type Delivery struct {
	From int
	To   int
	Msg  Message
}

// 可区分的拒绝原因。调用方可用 errors.Is 判定。
var (
	ErrEndpointMissing = errors.New("dining: edge endpoint does not exist")
	ErrSelfLoop        = errors.New("dining: self loop edge is not allowed")
	ErrDuplicateEdge   = errors.New("dining: duplicate edge")
	ErrNoSuchProcess   = errors.New("dining: process does not exist")
	ErrNotHungry       = errors.New("dining: process is not hungry")
	ErrNotEating       = errors.New("dining: process is not eating")
	ErrMissingFork     = errors.New("dining: process does not hold every fork")
	ErrHungryToThink   = errors.New("dining: hungry process cannot go back to thinking")
	ErrEatingToThink   = errors.New("dining: eating process must finish eating first")
	ErrAlreadyThinking = errors.New("dining: process is already thinking")
	ErrNoSuchMessage   = errors.New("dining: message is not pending at the channel head")
)

// Logger 是判定日志出口：每次操作/投递都打印输入、输出与判定依据。
type Logger interface {
	Log(Event)
}

// Event 记录一次操作的输入、输出与判定依据。
type Event struct {
	Seq      int64  // 全局单调序号
	Op       string // 操作名
	Input    string // 输入描述
	Output   string // 输出/结果状态描述
	Accepted bool   // 是否被接受
	Reason   string // 判定依据（接受时为规则依据，拒绝时为可区分原因）
}

// edgeState 是一条冲突边的内部状态。
type edgeState struct {
	a, b    int // a < b
	forkAt  int // 叉的持有者；-1 表示叉在途
	dirty   bool
	tokenAt int  // 令牌持有者；-1 表示令牌在途（请求消息中）
	deferA  bool // 暂存的来自 a 的请求
	deferB  bool // 暂存的来自 b 的请求
}

// Coordinator 是并发安全的冲突协调器。
type Coordinator struct {
	mu    sync.Mutex
	procs map[int]State
	edges map[[2]int]*edgeState
	net   Network
	log   Logger
	seq   int64
}

// NewCoordinator 创建协调器。
// 初始定向：叉在标识较小的一端且为脏，令牌在另一端。
func NewCoordinator(processes []int, edges [][2]int, net Network, logger Logger) (*Coordinator, error) {
	c := &Coordinator{
		procs: map[int]State{},
		edges: map[[2]int]*edgeState{},
		net:   net,
		log:   logger,
	}
	for _, p := range processes {
		c.procs[p] = Thinking
	}
	for _, raw := range edges {
		x, y := raw[0], raw[1]
		// 自环整体拒绝。
		if x == y {
			c.emit("NewCoordinator", fmt.Sprintf("edge %v", raw), "rejected", false,
				ErrSelfLoop.Error())
			return nil, ErrSelfLoop
		}
		// 边端点必须存在。
		if _, ok := c.procs[x]; !ok {
			c.emit("NewCoordinator", fmt.Sprintf("edge %v", raw), "rejected", false,
				ErrEndpointMissing.Error())
			return nil, fmt.Errorf("%w: %d", ErrEndpointMissing, x)
		}
		if _, ok := c.procs[y]; !ok {
			c.emit("NewCoordinator", fmt.Sprintf("edge %v", raw), "rejected", false,
				ErrEndpointMissing.Error())
			return nil, fmt.Errorf("%w: %d", ErrEndpointMissing, y)
		}
		key, _ := canonEdge(x, y)
		// 重复边（含方向重复）整体拒绝。
		if _, dup := c.edges[key]; dup {
			c.emit("NewCoordinator", fmt.Sprintf("edge %v", raw), "rejected", false,
				ErrDuplicateEdge.Error())
			return nil, fmt.Errorf("%w: %v", ErrDuplicateEdge, raw)
		}
		// 初始定向：叉在小端且为脏，令牌在大端。
		c.edges[key] = &edgeState{
			a:       key[0],
			b:       key[1],
			forkAt:  key[0],
			dirty:   true,
			tokenAt: key[1],
		}
	}
	c.emit("NewCoordinator",
		fmt.Sprintf("processes=%v edges=%v", processes, edges),
		"accepted: initial orientation installed", true,
		"fork at smaller endpoint and dirty; token at other endpoint")
	return c, nil
}
