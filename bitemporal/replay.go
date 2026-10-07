package bitemporal

import "sort"

// ReplayResult 是一次双时态回放的结果。
type ReplayResult struct {
	Edges   []Edge
	Defects []MirrorDefect
}

// Replay 回放在 (recordTime, validTime) 时刻实际存在的链接集合。
//
// 一条边仅当两个物理半在该双时态点同时存活时才呈现；
// 对称链接自动呈现为两条方向相反的有向边。
// 仅单侧存活（历史数据缺陷）不假装对称，而是作为 Defect 显式报告。
func (s *Snapshot) Replay(linkType ID, recordTime, validTime int64) ReplayResult {
	return s.replay(linkType, recordTime, validTime, nil)
}

func (s *Snapshot) replay(linkType ID, recordTime, validTime int64, probe *ProbeCounts) ReplayResult {
	idx := s.index[linkType]
	if idx == nil {
		return ReplayResult{}
	}
	// 为探针换一个共享计数的索引视图（底层结构相同，不发生重建）。
	if probe != nil {
		idx = idx.withProbe(probe)
	}
	alive := idx.rect.stab(recordTime, validTime)
	// stab 只返回存活半；为了区分“对象对完全没有事实”与“只有一个半存活”，
	// 对每个登记过的对象对分别探测其两个半（每个半的穿刺代价为 O(log^2 F)）。

	var edges []Edge
	var defects []MirrorDefect
	for pair := 0; pair*2+1 < len(idx.codes); pair++ {
		pos, neg := 2*pair, 2*pair+1
		hasPos, hasNeg := halfAlive(idx, pos, recordTime, validTime, alive),
			halfAlive(idx, neg, recordTime, validTime, alive)
		hc := idx.codes[pos]
		switch {
		case hasPos && hasNeg:
			if idx.lt.Symmetric {
				edges = append(edges,
					Edge{LinkType: linkType, From: hc.a, To: hc.b},
					Edge{LinkType: linkType, From: hc.b, To: hc.a})
			} else {
				edges = append(edges, Edge{LinkType: linkType, From: hc.a, To: hc.b})
			}
		case hasPos || hasNeg:
			missingCode := pos
			if hasPos {
				missingCode = neg
			}
			mc := idx.codes[missingCode]
			defects = append(defects, MirrorDefect{
				LinkType:    linkType,
				A:           hc.a,
				B:           hc.b,
				MissingSide: mc.side,
				ValidTime:   validTime,
				RecordTime:  recordTime,
			})
		}
	}

	sortEdges(edges)
	sort.Slice(defects, func(i, j int) bool {
		if defects[i].A != defects[j].A {
			return defects[i].A < defects[j].A
		}
		return defects[i].B < defects[j].B
	})
	return ReplayResult{Edges: edges, Defects: defects}
}

// halfAlive 判断单个物理半在指定双时态点是否存活。
// alive 是全量穿刺结果；若未命中则直接为 false，避免对死亡半重复穿刺。
func halfAlive(idx *linkIndex, code int, rt, vt int64, alive map[int]bool) bool {
	return alive[code]
}

func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
}

// CheckMirror 对账指定链接类型在该双时态时刻的正向/反向镜像一致性。
// forward 与 reverse 必须严格互为镜像；任何结构性不一致体现在 defects 中。
func (s *Snapshot) CheckMirror(linkType ID, recordTime, validTime int64) (forward, reverse []Edge, defects []MirrorDefect) {
	res := s.Replay(linkType, recordTime, validTime)
	lt, ok := s.linkType(linkType)
	if !ok {
		return nil, nil, res.Defects
	}
	for _, e := range res.Edges {
		if lt.Symmetric {
			forward = append(forward, e)
			reverse = append(reverse, Edge{LinkType: e.LinkType, From: e.To, To: e.From})
		} else {
			forward = append(forward, e)
			reverse = append(reverse, Edge{LinkType: e.LinkType, From: e.To, To: e.From})
		}
	}
	sortEdges(forward)
	sortEdges(reverse)
	return forward, reverse, res.Defects
}

// withProbe 返回一个共享全部只读结构但携带探针的浅拷贝索引。
func (idx *linkIndex) withProbe(p *ProbeCounts) *linkIndex {
	cp := *idx
	cp.rect = &rectIndex{rtTicks: idx.rect.rtTicks, tree: idx.rect.tree, probe: p}
	return &cp
}
