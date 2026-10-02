package sps

import "sort"

const inf int64 = 1<<62 - 1

// examMeter 统计一次更新中实际扫描的边条数。
type examMeter struct{ n int }

func (m *examMeter) edges(k int) { m.n += k }

func cloneDist(d []int64) []int64 {
	out := make([]int64, len(d))
	copy(out, d)
	return out
}

func sortInts(a []int) { sort.Ints(a) }

// checkConsistency 在持读锁下校验全部不变量，供测试使用。返回非空描述表示不一致。
func (svc *Service) checkConsistency() string {
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if svc.dist[svc.source] != 0 || svc.par[svc.source] != 0 {
		return "source distance/parent wrong"
	}
	for v := 0; v < svc.n; v++ {
		if svc.dist[v] == inf {
			if svc.par[v] != 0 {
				return "unreachable node has parent"
			}
			continue
		}
		if v == svc.source {
			continue
		}
		pid := svc.par[v]
		if pid == 0 {
			return "reachable non-source node without parent"
		}
		pe := svc.edges[pid]
		if pe == nil || !pe.alive {
			return "parent edge missing or dead"
		}
		if pe.v != v || svc.dist[pe.u] == inf || svc.dist[pe.u]+pe.w != svc.dist[v] {
			return "parent edge not tight"
		}
		// 父边编号最小性。
		for _, id := range svc.in[v] {
			if id < pid && svc.tightLocked(svc.edges[id]) {
				return "parent not the minimum-id tight edge"
			}
		}
	}
	return ""
}

// New 构造服务：N 个节点、源点 s、存活边上限 emax、历史保留数 k。
func New(n, s, emax, k int) (*Service, error) {
	if n < 1 || n > 2000 || s < 0 || s >= n || emax < 1 || emax > 100000 || k < 1 || k > 64 {
		return nil, ErrInvalidArgument
	}
	svc := &Service{
		n:      n,
		source: s,
		emax:   emax,
		k:      k,
		nextID: 1,
		edges:  make(map[int]*edge),
		in:     make([][]int, n),
		out:    make([][]int, n),
		dist:   make([]int64, n),
		par:    make([]int, n),
	}
	for i := range svc.dist {
		svc.dist[i] = inf
	}
	svc.dist[s] = 0
	svc.initialDist = cloneDist(svc.dist)
	return svc, nil
}

func (svc *Service) insertAdj(e *edge) {
	e.posIn = len(svc.in[e.v])
	svc.in[e.v] = append(svc.in[e.v], e.id)
	e.posOut = len(svc.out[e.u])
	svc.out[e.u] = append(svc.out[e.u], e.id)
}

func (svc *Service) eraseAdj(e *edge) {
	inList := svc.in[e.v]
	last := svc.edges[inList[len(inList)-1]]
	inList[e.posIn] = last.id
	last.posIn = e.posIn
	svc.in[e.v] = inList[:len(inList)-1]

	outList := svc.out[e.u]
	lastOut := svc.edges[outList[len(outList)-1]]
	outList[e.posOut] = lastOut.id
	lastOut.posOut = e.posOut
	svc.out[e.u] = outList[:len(outList)-1]
}

// tightLocked 判定边 e 当前是否为紧边；调用方持锁。
func (svc *Service) tightLocked(e *edge) bool {
	return e.alive && svc.dist[e.u] != inf && svc.dist[e.v] != inf &&
		svc.dist[e.u]+e.w == svc.dist[e.v]
}

// bestParentLocked 返回指向 v 的编号最小紧边；调用方持锁。
func (svc *Service) bestParentLocked(v int, meter *examMeter) (int, bool) {
	if v == svc.source {
		return 0, false
	}
	best := 0
	for _, id := range svc.in[v] {
		meter.edges(1)
		e := svc.edges[id]
		if svc.tightLocked(e) && (best == 0 || e.id < best) {
			best = e.id
		}
	}
	return best, best != 0
}

// saveSnapshot 在版本号已经自增之后保存当前 dist 快照。
func (svc *Service) saveSnapshot() {
	snap := snapshot{version: svc.version, dist: cloneDist(svc.dist)}
	if len(svc.ring) < svc.k {
		svc.ring = append(svc.ring, snap)
		return
	}
	// 环形覆盖最旧版本。
	idx := (svc.version - 1) % svc.k
	svc.ring[idx] = snap
}

func (svc *Service) oldestVersion() int {
	return svc.version - len(svc.ring) + 1
}
