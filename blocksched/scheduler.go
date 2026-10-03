package blocksched

import (
	"errors"
	"sync"
)

var (
	// ErrClock 表示调用携带的 now 小于此前任何调用的 now。
	ErrClock = errors.New("blocksched: clock moved backwards")
	// ErrPeerExists 表示 AddPeer 收到了仍存在的对端 id。
	ErrPeerExists = errors.New("blocksched: peer already exists")
	// ErrBanned 表示对端因校验失败达到阈值而被封禁（或封禁记录仍在）。
	ErrBanned = errors.New("blocksched: peer is banned")
	// ErrBadArg 表示参数不合法（空 id、have 长度错误、块号越界等）。
	ErrBadArg = errors.New("blocksched: bad argument")
	// ErrNoPeer 表示对端不存在（从未添加、已 Drop，或封禁后已 Drop）。
	ErrNoPeer = errors.New("blocksched: no such peer")
	// ErrNoRequest 表示该对端在指定块上没有在途请求。
	ErrNoRequest = errors.New("blocksched: no such inflight request")
)

// Config 描述调度器的固定参数。
type Config struct {
	Blocks         int   // B：块数，1..4096
	BaseConcurrent int   // K：每对端基础并发，1..16
	GlobalLimit    int   // G：全局在途上限，>=1
	MaxBlockDup    int   // M：同一块最大并发请求数，>=2
	Timeout        int64 // T：请求超时阈值，>=1
	BanThreshold   int   // F：校验失败封禁阈值，>=1
}

// request 是一条在途请求。
type request struct {
	issued int64
}

// peer 记录单个对端的状态。
type peer struct {
	id       string
	have     []bool
	timeouts int
	fails    map[int]struct{}
	inflight map[int]*request
	banned   bool
}

type inflightKey struct {
	id string
	b  int
}

// Timeout 描述一条被 Tick 判定为过期的请求。
type Timeout struct {
	Issued int64
	Peer   string
	Block  int
}

// Scheduler 是多对端块拉取调度器；零值不可用，请用 New 构造。
type Scheduler struct {
	mu sync.Mutex

	b int
	k int
	g int
	m int
	t int64
	f int

	now         int64
	peers       map[string]*peer
	banned      map[string]struct{}
	complete    []bool
	availCount  []int
	blockFlight []int
	totalFlight int
	inflight    map[inflightKey]*request
}

// New 创建调度器；参数越界时 panic。
func New(cfg Config) *Scheduler {
	if cfg.Blocks < 1 || cfg.Blocks > 4096 ||
		cfg.BaseConcurrent < 1 || cfg.BaseConcurrent > 16 ||
		cfg.GlobalLimit < 1 ||
		cfg.MaxBlockDup < 2 ||
		cfg.Timeout < 1 ||
		cfg.BanThreshold < 1 {
		panic("blocksched: invalid config")
	}
	return &Scheduler{
		b:           cfg.Blocks,
		k:           cfg.BaseConcurrent,
		g:           cfg.GlobalLimit,
		m:           cfg.MaxBlockDup,
		t:           cfg.Timeout,
		f:           cfg.BanThreshold,
		peers:       make(map[string]*peer),
		banned:      make(map[string]struct{}),
		complete:    make([]bool, cfg.Blocks),
		availCount:  make([]int, cfg.Blocks),
		blockFlight: make([]int, cfg.Blocks),
		inflight:    make(map[inflightKey]*request),
	}
}

// Complete 报告全部块是否已完成。
func (s *Scheduler) Complete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeAllLocked()
}
