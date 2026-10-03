// Package roadnet 实现带通告历史的时间依赖路网最早到达查询服务。
//
// 路网为有向图，边的通行耗时随出发时刻分段变化，可封闭（耗时 -1），
// 并可通过 Announce 随时间发布新的耗时剖面。所有变更与查询均可并发调用，
// 通过读写锁保证结果等价于某个串行顺序；查询可按版本号读取历史快照，
// 同一版本的查询结果永远可精确复现。
package roadnet

import (
	"fmt"
	"sync"
)

const (
	// MaxNodes 是节点数 N 的上限。
	MaxNodes = 5000
	// MaxEdges 是边数上限 E 的上限。
	MaxEdges = 100000
	// MaxTime 是合法时刻的上限（含）。
	MaxTime = int64(1_000_000_000)
	// MaxCost 是单段通行耗时的上限（含）。
	MaxCost = int64(1_000_000)
	// MaxSegments 是一条耗时剖面的最大分段数。
	MaxSegments = 32
	// MaxRecords 是每条边至多保存的生效通告记录数（被替换的不计）。
	MaxRecords = 64
	// ClosedCost 表示该分段封闭，段内不能开始通行。
	ClosedCost = int64(-1)
)

// Segment 是耗时剖面的一段：自（记录生效时刻 + Offset）起，到下一段起点
// （或下一条记录的生效时刻）前，通行耗时为 Cost；Cost 为 ClosedCost 表示封闭。
type Segment struct {
	Offset int64
	Cost   int64
}

// Record 是一条通告记录。被同 eff 替换的旧记录不删除，
// 仅记下被替换时的版本 ReplVersion（0 表示未被替换）。
type Record struct {
	Eff         int64
	Profile     []Segment
	RegVersion  int64
	ReplVersion int64
}

// Edge 是一条有向边，允许平行边，不允许自环。
type Edge struct {
	ID         int
	U, V       int
	RegVersion int64
	Records    []Record // eff 非降；被替换的记录保留在原位
}

// liveCount 返回当前生效（未被替换）的记录条数。
func (e *Edge) liveCount() int {
	c := 0
	for i := range e.Records {
		if e.Records[i].ReplVersion == 0 {
			c++
		}
	}
	return c
}

// Network 是带通告历史的时间依赖路网。零值不可用，请用 New 构造。
type Network struct {
	mu      sync.RWMutex
	n       int
	maxE    int
	now     int64
	version int64
	edges   []*Edge // edges[i] 的编号为 i+1
	adj     [][]int // adj[u] 为从 u 出发的边编号（按登记顺序）
}

// New 构造 n 个节点（编号 0..n-1）、边数上限 maxE 的空路网。
func New(n, maxE int) (*Network, error) {
	if n < 1 || n > MaxNodes || maxE < 1 || maxE > MaxEdges {
		return nil, &OpError{Op: "New", Code: ErrInvalidParam,
			Msg: fmt.Sprintf("节点数 N=%d 或边数上限 E=%d 越界（N∈[1,%d]，E∈[1,%d]）", n, maxE, MaxNodes, MaxEdges)}
	}
	return &Network{n: n, maxE: maxE, adj: make([][]int, n)}, nil
}

// Now 返回系统时钟。
func (nw *Network) Now() int64 {
	nw.mu.RLock()
	defer nw.mu.RUnlock()
	return nw.now
}

// Version 返回当前版本号。
func (nw *Network) Version() int64 {
	nw.mu.RLock()
	defer nw.mu.RUnlock()
	return nw.version
}

// EdgeCount 返回已登记的边数（即下一条边的编号减一）。
func (nw *Network) EdgeCount() int {
	nw.mu.RLock()
	defer nw.mu.RUnlock()
	return len(nw.edges)
}

func validProfile(p []Segment) bool {
	if len(p) < 1 || len(p) > MaxSegments {
		return false
	}
	if p[0].Offset != 0 {
		return false
	}
	for i := range p {
		if p[i].Offset < 0 || p[i].Offset > MaxTime {
			return false
		}
		if i > 0 && p[i].Offset <= p[i-1].Offset {
			return false
		}
		if c := p[i].Cost; c != ClosedCost && (c < 1 || c > MaxCost) {
			return false
		}
	}
	return true
}

func cloneProfile(p []Segment) []Segment {
	q := make([]Segment, len(p))
	copy(q, p)
	return q
}

// AddEdge 登记一条 u→v 的边，附带一条 eff 为 0 的通告记录。
// 成功返回边编号（从 1 起严格递增，被拒绝的加边不消耗编号）。
func (nw *Network) AddEdge(u, v int, profile []Segment) (int, error) {
	nw.mu.Lock()
	defer nw.mu.Unlock()
	if u < 0 || u >= nw.n || v < 0 || v >= nw.n || u == v || !validProfile(profile) {
		return 0, &OpError{Op: "AddEdge", Code: ErrInvalidParam,
			Msg: fmt.Sprintf("u=%d v=%d 或剖面 %v 非法", u, v, profile)}
	}
	if len(nw.edges) >= nw.maxE {
		return 0, &OpError{Op: "AddEdge", Code: ErrEdgeLimit,
			Msg: fmt.Sprintf("边数已达上限 %d", nw.maxE)}
	}
	nw.version++
	id := len(nw.edges) + 1
	e := &Edge{
		ID:         id,
		U:          u,
		V:          v,
		RegVersion: nw.version,
		Records: []Record{{
			Eff:        0,
			Profile:    cloneProfile(profile),
			RegVersion: nw.version,
		}},
	}
	nw.edges = append(nw.edges, e)
	nw.adj[u] = append(nw.adj[u], id)
	return id, nil
}

// Announce 为边 edgeID 追加一条通告记录；eff 恰等于最后一条记录的 eff 时
// 替换该记录（旧记录保留并记下被替换版本），否则追加。
func (nw *Network) Announce(edgeID int, eff int64, profile []Segment) error {
	nw.mu.Lock()
	defer nw.mu.Unlock()
	if edgeID < 1 || eff < 0 || eff > MaxTime || !validProfile(profile) {
		return &OpError{Op: "Announce", Code: ErrInvalidParam,
			Msg: fmt.Sprintf("边编号 %d、生效时刻 %d 或剖面 %v 非法", edgeID, eff, profile)}
	}
	if edgeID > len(nw.edges) {
		return &OpError{Op: "Announce", Code: ErrEdgeNotFound,
			Msg: fmt.Sprintf("边编号 %d 未分配", edgeID)}
	}
	e := nw.edges[edgeID-1]
	if eff < nw.now {
		return &OpError{Op: "Announce", Code: ErrRetroactive,
			Msg: fmt.Sprintf("生效时刻 %d 早于当前时钟 %d，不能改写过去", eff, nw.now)}
	}
	last := &e.Records[len(e.Records)-1] // 末尾记录一定未被替换
	if eff < last.Eff {
		return &OpError{Op: "Announce", Code: ErrRecordOrder,
			Msg: fmt.Sprintf("生效时刻 %d 小于该边最后一条记录的 %d", eff, last.Eff)}
	}
	if eff == last.Eff {
		nw.version++
		last.ReplVersion = nw.version
		e.Records = append(e.Records, Record{
			Eff:        eff,
			Profile:    cloneProfile(profile),
			RegVersion: nw.version,
		})
		return nil
	}
	if e.liveCount() >= MaxRecords {
		return &OpError{Op: "Announce", Code: ErrRecordLimit,
			Msg: fmt.Sprintf("该边已有 %d 条生效记录", MaxRecords)}
	}
	nw.version++
	e.Records = append(e.Records, Record{
		Eff:        eff,
		Profile:    cloneProfile(profile),
		RegVersion: nw.version,
	})
	return nil
}

// Advance 把系统时钟推进到 t（不增加版本号）。
func (nw *Network) Advance(t int64) error {
	nw.mu.Lock()
	defer nw.mu.Unlock()
	if t < 0 || t > MaxTime {
		return &OpError{Op: "Advance", Code: ErrInvalidParam,
			Msg: fmt.Sprintf("时刻 %d 越界 [0,%d]", t, MaxTime)}
	}
	if t < nw.now {
		return &OpError{Op: "Advance", Code: ErrClockRewind,
			Msg: fmt.Sprintf("时刻 %d 早于当前时钟 %d，时钟不能回退", t, nw.now)}
	}
	nw.now = t
	return nil
}
