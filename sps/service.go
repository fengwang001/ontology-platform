// Package sps 提供带版本历史的增量单源最短路维护服务。
package sps

import (
	"sync"
)

// 可区分的错误原因。
var (
	ErrInvalidArgument = spsError("invalid argument")
	ErrEdgeNotFound    = spsError("edge not found")
	ErrCapacityFull    = spsError("capacity full")
	ErrVersionStale    = spsError("history version expired")
	ErrVersionFuture   = spsError("version not produced yet")
	ErrUnreachable     = spsError("node unreachable from source")
)

type spsError string

func (e spsError) Error() string { return string(e) }

const (
	maxWeight = 1_000_000
)

type edge struct {
	id     int
	u, v   int
	w      int64
	alive  bool
	posIn  int // 在 in[v] 中的下标
	posOut int // 在 out[u] 中的下标
}

// UpdateResult 是一次被接受的更新操作的报告。
type UpdateResult struct {
	Version  int
	EdgeID   int // 仅 AddEdge 使用：新分配的边编号；其余为 0
	DChanged []int
	PChanged []int
	Examined int
}

type snapshot struct {
	version int
	dist    []int64 // INF 表示不可达
}

// Service 是增量单源最短路维护服务。零值不可用，请用 New 构造。
type Service struct {
	mu     sync.RWMutex
	n      int
	source int
	emax   int
	k      int

	liveCount int
	nextID    int

	edges map[int]*edge
	in    [][]int
	out   [][]int

	dist []int64
	par  []int // 0 表示无父边

	version int
	// initialDist 是版本 0 的距离（除源点外全部不可达），版本 0 始终可查。
	initialDist []int64
	// 环形缓冲，保存最近至多 k 个“已接受更新后”的版本快照。
	ring    []snapshot
}

// AddEdge 新增一条有向正权边。
func (svc *Service) AddEdge(u, v int, w int64) (*UpdateResult, error) {
	if u < 0 || u >= svc.n || v < 0 || v >= svc.n || u == v || w < 1 || w > maxWeight {
		return nil, ErrInvalidArgument
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.liveCount >= svc.emax {
		return nil, ErrCapacityFull
	}
	id := svc.nextID
	e := &edge{id: id, u: u, v: v, w: w, alive: true}
	svc.edges[id] = e
	svc.insertAdj(e)
	svc.liveCount++
	svc.nextID++

	meter := &examMeter{}
	dc, pc := svc.applyDecrease(e, meter)
	svc.commit()
	return &UpdateResult{
		Version:  svc.version,
		EdgeID:   id,
		DChanged: dc,
		PChanged: pc,
		Examined: meter.n,
	}, nil
}

// SetWeight 修改指定边的权重。
func (svc *Service) SetWeight(id int, w int64) (*UpdateResult, error) {
	if w < 1 || w > maxWeight {
		return nil, ErrInvalidArgument
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	e, ok := svc.edges[id]
	if !ok || !e.alive {
		return nil, ErrEdgeNotFound
	}

	oldW := e.w
	wasTight := svc.tightLocked(e)
	meter := &examMeter{}

	var dc, pc []int
	if w < oldW {
		e.w = w
		dc, pc = svc.applyDecrease(e, meter)
	} else if w > oldW {
		e.w = w
		dc, pc = svc.applyIncrease(e, wasTight, meter)
	}
	// w == oldW：接受但不产生任何变化。

	svc.commit()
	return &UpdateResult{
		Version:  svc.version,
		DChanged: dc,
		PChanged: pc,
		Examined: meter.n,
	}, nil
}

// RemoveEdge 删除指定边。
func (svc *Service) RemoveEdge(id int) (*UpdateResult, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	e, ok := svc.edges[id]
	if !ok || !e.alive {
		return nil, ErrEdgeNotFound
	}

	wasTight := svc.tightLocked(e)
	meter := &examMeter{}

	e.alive = false
	dc, pc := svc.applyIncrease(e, wasTight, meter)
	svc.eraseAdj(e)
	delete(svc.edges, id)
	svc.liveCount--
	svc.commit()
	return &UpdateResult{
		Version:  svc.version,
		DChanged: dc,
		PChanged: pc,
		Examined: meter.n,
	}, nil
}

// Dist 返回当前版本下 v 的距离与是否可达。
func (svc *Service) Dist(v int) (int64, bool, error) {
	if v < 0 || v >= svc.n {
		return 0, false, ErrInvalidArgument
	}
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if svc.dist[v] == inf {
		return 0, false, nil
	}
	return svc.dist[v], true, nil
}

// Parent 返回当前版本下 v 的父边编号与是否存在。
func (svc *Service) Parent(v int) (int, bool, error) {
	if v < 0 || v >= svc.n {
		return 0, false, ErrInvalidArgument
	}
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if svc.par[v] == 0 {
		return 0, false, nil
	}
	return svc.par[v], true, nil
}

// Path 返回从源点到 v 的父边编号序列（沿父边回溯，顺序为源到 v）。
func (svc *Service) Path(v int) ([]int, error) {
	if v < 0 || v >= svc.n {
		return nil, ErrInvalidArgument
	}
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if svc.dist[v] == inf {
		return nil, ErrUnreachable
	}
	rev := make([]int, 0)
	for x := v; x != svc.source; {
		pid := svc.par[x]
		if pid == 0 {
			// 理论上不可达：防御性处理。
			return nil, ErrUnreachable
		}
		rev = append(rev, pid)
		e := svc.edges[pid]
		x = e.u
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

// DistAt 返回历史版本 ver 下 v 的距离与是否可达。
func (svc *Service) DistAt(v, ver int) (int64, bool, error) {
	if v < 0 || v >= svc.n {
		return 0, false, ErrInvalidArgument
	}
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if ver > svc.version {
		return 0, false, ErrVersionFuture
	}
	oldest := svc.oldestVersion()
	if ver < oldest {
		return 0, false, ErrVersionStale
	}
	if ver == 0 {
		d := svc.initialDist[v]
		if d == inf {
			return 0, false, nil
		}
		return d, true, nil
	}
	snap := svc.ring[(ver-1)%svc.k]
	d := snap.dist[v]
	if d == inf {
		return 0, false, nil
	}
	return d, true, nil
}

// Version 返回当前版本号。
func (svc *Service) Version() int {
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	return svc.version
}

// commit 在一次被接受的更新完成后推进版本并写入历史快照。
func (svc *Service) commit() {
	svc.version++
	svc.saveSnapshot()
}
